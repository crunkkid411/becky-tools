package audiohost

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"becky-go/internal/seam"
)

// recordedCall captures one command/query the client emitted, so a test can
// assert the verb name, message type, and args the client sent.
type recordedCall struct {
	typ  seam.MessageType
	name string
	args map[string]interface{}
}

// recorder is a fake-sidecar handler that records every call and returns a
// canned response keyed by verb. Safe for concurrent use (the seam pump may
// invoke it from its own goroutine).
type recorder struct {
	mu        sync.Mutex
	calls     []recordedCall
	responses map[string]interface{} // verb -> data payload
	errs      map[string]string      // verb -> error string (ok:false)
}

func newRecorder() *recorder {
	return &recorder{
		responses: map[string]interface{}{},
		errs:      map[string]string{},
	}
}

func (r *recorder) handler(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
	var parsed map[string]interface{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &parsed)
	}
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{typ: typ, name: name, args: parsed})
	r.mu.Unlock()

	if msg, bad := r.errs[name]; bad {
		return nil, msg
	}
	if data, ok := r.responses[name]; ok {
		return data, ""
	}
	// Default: succeed with empty object so void verbs don't error.
	return map[string]interface{}{}, ""
}

// last returns the most recent recorded call; fails the test if none.
func (r *recorder) last(t *testing.T) recordedCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	return r.calls[len(r.calls)-1]
}

// newTestClient wires a Client to a fake seam sidecar driven by rec.
//
// We deliberately do NOT EmitReady here: the audiohost client never consumes the
// Events() channel, so an un-drained "ready" event left buffered when the fake
// is Closed would race the seam pump against seam's own (pre-existing) shutdown
// channel-close. The ready handshake is irrelevant to request/response calls, so
// omitting it keeps these unit tests clean under -race without touching the
// shared internal/seam package. A dedicated test exercises the ready path with a
// proper drain.
func newTestClient(t *testing.T, rec *recorder) (*Client, *seam.FakeSidecar) {
	t.Helper()
	fake := seam.NewFakeSidecar(rec.handler)
	c := newClientFromTransport(fake.Controller())
	return c, fake
}

func ctx() context.Context { return context.Background() }

// --- per-verb tests: assert verb name, type, args, and response parsing ---

func TestPing(t *testing.T) {
	rec := newRecorder()
	rec.responses["ping"] = map[string]interface{}{"pong": true, "version": "0.1.0"}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	v, err := c.Ping(ctx())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if v != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", v)
	}
	call := rec.last(t)
	if call.name != "ping" {
		t.Errorf("verb = %q, want ping", call.name)
	}
	if call.typ != seam.TypeQuery {
		t.Errorf("type = %q, want query", call.typ)
	}
}

func TestDevices(t *testing.T) {
	rec := newRecorder()
	rec.responses["audio.devices"] = map[string]interface{}{
		"default_output":   1,
		"default_host_api": "WASAPI",
		"asio_available":   true,
		"devices": []map[string]interface{}{
			{"index": 0, "name": "Speakers", "hostApi": "WASAPI"},
			{"index": 1, "name": "Steinberg UR12", "hostApi": "WASAPI", "default": true},
		},
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	d, err := c.Devices(ctx())
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if d.DefaultHostAPI != "WASAPI" || !d.ASIOAvailable || d.DefaultOutput != 1 {
		t.Errorf("decoded header wrong: %+v", d)
	}
	if len(d.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(d.Devices))
	}
	if d.Devices[1].Name != "Steinberg UR12" || !d.Devices[1].Default {
		t.Errorf("device[1] decode wrong: %+v", d.Devices[1])
	}
	call := rec.last(t)
	if call.name != "audio.devices" || call.typ != seam.TypeQuery {
		t.Errorf("call = %+v, want query audio.devices", call)
	}
}

func TestOpenAudio(t *testing.T) {
	rec := newRecorder()
	rec.responses["audio.open"] = map[string]interface{}{
		"device": 1, "samplerate": 48000, "buffer": 256, "hostApi": "WASAPI",
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	dev, sr, buf := 1, 48000, 256
	r, err := c.OpenAudio(ctx(), OpenOptions{Device: &dev, SampleRate: &sr, Buffer: &buf})
	if err != nil {
		t.Fatalf("OpenAudio: %v", err)
	}
	if r.SampleRate != 48000 || r.Buffer != 256 {
		t.Errorf("decoded open wrong: %+v", r)
	}
	call := rec.last(t)
	if call.name != "audio.open" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v, want command audio.open", call)
	}
	if got := call.args["device"]; got != float64(1) {
		t.Errorf("device arg = %v, want 1", got)
	}
	if got := call.args["samplerate"]; got != float64(48000) {
		t.Errorf("samplerate arg = %v, want 48000", got)
	}
}

func TestOpenAudioDefaults(t *testing.T) {
	rec := newRecorder()
	rec.responses["audio.open"] = map[string]interface{}{"device": 0, "samplerate": 48000, "buffer": 512}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if _, err := c.OpenAudio(ctx(), OpenOptions{}); err != nil {
		t.Fatalf("OpenAudio defaults: %v", err)
	}
	call := rec.last(t)
	// With nil pointers + omitempty, no device/samplerate/buffer should be sent.
	if _, ok := call.args["device"]; ok {
		t.Errorf("expected no device arg when default, got %v", call.args)
	}
}

func TestStartStopAudio(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if err := c.StartAudio(ctx()); err != nil {
		t.Fatalf("StartAudio: %v", err)
	}
	if call := rec.last(t); call.name != "audio.start" || call.typ != seam.TypeCommand {
		t.Errorf("start call = %+v", call)
	}
	if err := c.StopAudio(ctx()); err != nil {
		t.Fatalf("StopAudio: %v", err)
	}
	if call := rec.last(t); call.name != "audio.stop" || call.typ != seam.TypeCommand {
		t.Errorf("stop call = %+v", call)
	}
}

func TestScanVST(t *testing.T) {
	rec := newRecorder()
	rec.responses["vst.scan"] = map[string]interface{}{
		"dir":     `C:\Program Files\Common Files\VST3`,
		"count":   2,
		"crashed": 1,
		"plugins": []map[string]interface{}{
			{
				"path":     `C:\Program Files\Common Files\VST3\Serum.vst3`,
				"name":     "Serum",
				"loadable": true,
				"category": "Instrument",
				"classes": []map[string]interface{}{
					{"name": "Serum", "category": "Instrument", "vendor": "Xfer", "version": "1.0"},
				},
			},
			{
				"path":     `C:\Program Files\Common Files\VST3\Broken.vst3`,
				"name":     "Broken",
				"loadable": false,
				"crashed":  true,
				"error":    "faulted on load",
			},
		},
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	res, err := c.ScanVST(ctx(), `C:\VST3`)
	if err != nil {
		t.Fatalf("ScanVST: %v", err)
	}
	if res.Count != 2 || res.Crashed != 1 {
		t.Errorf("count/crashed wrong: %+v", res)
	}
	if len(res.Plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(res.Plugins))
	}
	p := res.Plugins[0]
	if p.Name != "Serum" || !p.Loadable || p.Category != "Instrument" {
		t.Errorf("plugin[0] decode wrong: %+v", p)
	}
	if len(p.Classes) != 1 || p.Classes[0].Vendor != "Xfer" {
		t.Errorf("plugin[0] classes decode wrong: %+v", p.Classes)
	}
	if !res.Plugins[1].Crashed {
		t.Errorf("plugin[1] should be crashed: %+v", res.Plugins[1])
	}
	call := rec.last(t)
	if call.name != "vst.scan" || call.typ != seam.TypeQuery {
		t.Errorf("call = %+v, want query vst.scan", call)
	}
	if got := call.args["dir"]; got != `C:\VST3` {
		t.Errorf("dir arg = %v, want C:\\VST3", got)
	}
	if got := call.args["recursive"]; got != true {
		t.Errorf("recursive arg = %v, want true", got)
	}
}

func TestScanVSTDefaultDir(t *testing.T) {
	rec := newRecorder()
	rec.responses["vst.scan"] = map[string]interface{}{"dir": "x", "count": 0, "plugins": []interface{}{}}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if _, err := c.ScanVST(ctx(), ""); err != nil {
		t.Fatalf("ScanVST empty: %v", err)
	}
	call := rec.last(t)
	if _, ok := call.args["dir"]; ok {
		t.Errorf("empty dir should omit dir arg, got %v", call.args)
	}
}

func TestLoadVST(t *testing.T) {
	rec := newRecorder()
	rec.responses["vst.load"] = map[string]interface{}{
		"instanceId":  3,
		"name":        "Serum",
		"path":        `C:\VST3\Serum.vst3`,
		"outChannels": 2,
		"sampleRate":  48000,
		"hasEditor":   true,
		"params": []map[string]interface{}{
			{"id": 0, "title": "MasterLevel", "default": 0.5, "current": 0.5, "stepCount": 0},
		},
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	inst, err := c.LoadVST(ctx(), `C:\VST3\Serum.vst3`)
	if err != nil {
		t.Fatalf("LoadVST: %v", err)
	}
	if inst.InstanceID != 3 || inst.Name != "Serum" || !inst.HasEditor || inst.OutChannels != 2 {
		t.Errorf("instance decode wrong: %+v", inst)
	}
	if len(inst.Params) != 1 || inst.Params[0].Title != "MasterLevel" {
		t.Errorf("params decode wrong: %+v", inst.Params)
	}
	call := rec.last(t)
	if call.name != "vst.load" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v, want command vst.load", call)
	}
	if got := call.args["path"]; got != `C:\VST3\Serum.vst3` {
		t.Errorf("path arg = %v", got)
	}
}

func TestLoadVSTEmptyPath(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()
	if _, err := c.LoadVST(ctx(), ""); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestLoadVSTOptions(t *testing.T) {
	rec := newRecorder()
	rec.responses["vst.load"] = map[string]interface{}{"instanceId": 1, "name": "X"}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if _, err := c.LoadVSTOptions(ctx(), `C:\p.vst3`, 44100, 128); err != nil {
		t.Fatalf("LoadVSTOptions: %v", err)
	}
	call := rec.last(t)
	if got := call.args["samplerate"]; got != float64(44100) {
		t.Errorf("samplerate arg = %v, want 44100", got)
	}
	if got := call.args["buffer"]; got != float64(128) {
		t.Errorf("buffer arg = %v, want 128", got)
	}
}

func TestParamList(t *testing.T) {
	rec := newRecorder()
	rec.responses["vst.param.list"] = map[string]interface{}{
		"instanceId": 3,
		"params": []map[string]interface{}{
			{"id": 1, "title": "Cutoff", "default": 0.7, "current": 0.42},
		},
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	params, err := c.ParamList(ctx(), 3)
	if err != nil {
		t.Fatalf("ParamList: %v", err)
	}
	if len(params) != 1 || params[0].Title != "Cutoff" || params[0].Current != 0.42 {
		t.Errorf("params decode wrong: %+v", params)
	}
	call := rec.last(t)
	if call.name != "vst.param.list" || call.typ != seam.TypeQuery {
		t.Errorf("call = %+v, want query vst.param.list", call)
	}
	if got := call.args["instanceId"]; got != float64(3) {
		t.Errorf("instanceId arg = %v, want 3", got)
	}
}

func TestSetParam(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if err := c.SetParam(ctx(), 3, 7, 0.25); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	call := rec.last(t)
	if call.name != "vst.param.set" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v, want command vst.param.set", call)
	}
	if got := call.args["instanceId"]; got != float64(3) {
		t.Errorf("instanceId arg = %v", got)
	}
	if got := call.args["paramId"]; got != float64(7) {
		t.Errorf("paramId arg = %v, want 7", got)
	}
	if got := call.args["value"]; got != 0.25 {
		t.Errorf("value arg = %v, want 0.25", got)
	}
}

func TestNoteOnOff(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if err := c.NoteOn(ctx(), 3, 60, 0.9); err != nil {
		t.Fatalf("NoteOn: %v", err)
	}
	call := rec.last(t)
	if call.name != "note.on" || call.typ != seam.TypeCommand {
		t.Errorf("note.on call = %+v", call)
	}
	if call.args["pitch"] != float64(60) || call.args["velocity"] != 0.9 {
		t.Errorf("note.on args = %v", call.args)
	}

	if err := c.NoteOff(ctx(), 3, 60); err != nil {
		t.Fatalf("NoteOff: %v", err)
	}
	call = rec.last(t)
	if call.name != "note.off" || call.typ != seam.TypeCommand {
		t.Errorf("note.off call = %+v", call)
	}
	if call.args["pitch"] != float64(60) {
		t.Errorf("note.off pitch = %v", call.args["pitch"])
	}
}

func TestRender(t *testing.T) {
	rec := newRecorder()
	rec.responses["render"] = map[string]interface{}{
		"out": "out.wav", "name": "Serum", "frames": 96000, "channels": 2,
		"sampleRate": 48000, "peak": 0.8, "peakDb": -1.9, "rms": 0.2, "rmsDb": -14.0,
		"nonSilent": true, "isInstrument": true,
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	events := []NoteEvent{NoteOn(0, 60, 0.9), NoteOff(1.5, 60)}
	res, err := c.Render(ctx(), 3, events, 2.0, "out.wav", RenderOptions{SampleRate: 48000})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.NonSilent || res.Frames != 96000 || res.Channels != 2 || res.Name != "Serum" {
		t.Errorf("render decode wrong: %+v", res)
	}
	call := rec.last(t)
	if call.name != "render" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v, want command render", call)
	}
	if got := call.args["instanceId"]; got != float64(3) {
		t.Errorf("instanceId arg = %v, want 3", got)
	}
	if got := call.args["durationSec"]; got != 2.0 {
		t.Errorf("durationSec arg = %v, want 2.0", got)
	}
	if got := call.args["out"]; got != "out.wav" {
		t.Errorf("out arg = %v", got)
	}
	evs, ok := call.args["events"].([]interface{})
	if !ok || len(evs) != 2 {
		t.Fatalf("events arg wrong: %v", call.args["events"])
	}
	ev0 := evs[0].(map[string]interface{})
	if ev0["type"] != "noteOn" || ev0["pitch"] != float64(60) {
		t.Errorf("event[0] = %v", ev0)
	}
}

func TestRenderEmptyOut(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()
	if _, err := c.Render(ctx(), 3, nil, 2.0, "", RenderOptions{}); err == nil {
		t.Fatal("expected error on empty out path")
	}
}

func TestRenderPath(t *testing.T) {
	rec := newRecorder()
	rec.responses["render"] = map[string]interface{}{
		"out": "o.wav", "name": "Serum", "frames": 48000, "channels": 2,
		"nonSilent": true,
	}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	res, err := c.RenderPath(ctx(), `C:\VST3\Serum.vst3`, nil, 1.0, "o.wav", RenderOptions{})
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if !res.NonSilent {
		t.Errorf("expected nonSilent, got %+v", res)
	}
	call := rec.last(t)
	if call.name != "render" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v", call)
	}
	if got := call.args["path"]; got != `C:\VST3\Serum.vst3` {
		t.Errorf("path arg = %v", got)
	}
	if _, ok := call.args["instanceId"]; ok {
		t.Errorf("RenderPath should NOT send instanceId, got %v", call.args)
	}
}

func TestRenderPathEmpty(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()
	if _, err := c.RenderPath(ctx(), "", nil, 1.0, "o.wav", RenderOptions{}); err == nil {
		t.Fatal("expected error on empty plugin path")
	}
	if _, err := c.RenderPath(ctx(), `C:\p.vst3`, nil, 1.0, "", RenderOptions{}); err == nil {
		t.Fatal("expected error on empty out path")
	}
}

func TestShutdown(t *testing.T) {
	rec := newRecorder()
	rec.responses["shutdown"] = map[string]interface{}{"bye": true}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	if err := c.Shutdown(ctx()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if call := rec.last(t); call.name != "shutdown" || call.typ != seam.TypeCommand {
		t.Errorf("call = %+v, want command shutdown", call)
	}
}

// --- degrade-never-crash behaviour ---

func TestHostErrorIsReturnedNotPanicked(t *testing.T) {
	rec := newRecorder()
	rec.errs["vst.load"] = "cannot load module: bad.vst3"
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	_, err := c.LoadVST(ctx(), `C:\bad.vst3`)
	if err == nil {
		t.Fatal("expected error from host ok:false")
	}
}

func TestCallOnNilClient(t *testing.T) {
	c := &Client{} // no transport
	if _, err := c.Ping(ctx()); err == nil {
		t.Fatal("expected error calling Ping on unopened client")
	}
}

func TestCloseIdempotent(t *testing.T) {
	rec := newRecorder()
	c, fake := newTestClient(t, rec)
	defer fake.Close()
	c.Close()
	c.Close() // must not panic
}

func TestConcurrentCalls(t *testing.T) {
	rec := newRecorder()
	rec.responses["ping"] = map[string]interface{}{"pong": true, "version": "0.1.0"}
	c, fake := newTestClient(t, rec)
	defer fake.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Ping(ctx()); err != nil {
				t.Errorf("concurrent Ping: %v", err)
			}
		}()
	}
	wg.Wait()
	rec.mu.Lock()
	n := len(rec.calls)
	rec.mu.Unlock()
	if n != 20 {
		t.Errorf("recorded %d calls, want 20", n)
	}
}

// TestReadyEventCoexists proves the client works when the host emits the
// startup "ready" event: we drain the event off the controller's Events()
// channel before closing, so the seam pump never sends to a closed channel.
func TestReadyEventCoexists(t *testing.T) {
	rec := newRecorder()
	rec.responses["ping"] = map[string]interface{}{"pong": true, "version": "0.1.0"}
	fake := seam.NewFakeSidecar(rec.handler)
	fake.EmitReady("audio-host", "0.1.0")
	c := newClientFromTransport(fake.Controller())

	// Drain the ready event so nothing is left buffered when we close.
	select {
	case ev := <-fake.Controller().Events():
		if ev.Name != "ready" {
			t.Errorf("first event = %q, want ready", ev.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ready event")
	}

	if _, err := c.Ping(ctx()); err != nil {
		t.Fatalf("Ping after ready: %v", err)
	}
	c.Close()
	fake.Close()
}

// satisfy interface check at compile time.
var _ transport = (*seam.Sidecar)(nil)
