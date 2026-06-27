// otiocli.go — the OPTIONAL `--via-otio-cli` escape hatch (SPEC-BECKY-OTIO §7,
// Phase 2). becky emits .otio natively in Go and never needs Python; but IF the
// user has the OpenTimelineIO Python package installed, its `otioconvert` CLI can
// turn a generated .otio into adapters becky doesn't write itself (AAF, ALE, …).
// This is the ONLY exec in the otio package and it is strictly degrade-never-crash
// (CLAUDE.md §2): if otioconvert isn't on PATH the caller keeps the .otio and
// reports a note — becky never depends on Python being present.
package otio

import (
	"fmt"
	"os/exec"
	"strings"
)

// OtioConvert runs `otioconvert -i <otioPath> -o <outPath>` to reach an adapter
// format from an already-written .otio. Return contract:
//
//	(false, nil)  — otioconvert is not installed; degrade, keep the .otio.
//	(true,  nil)  — conversion succeeded; outPath was written.
//	(false, err)  — otioconvert is installed but the conversion failed (real error).
func OtioConvert(otioPath, outPath string) (ran bool, err error) {
	bin, lookErr := exec.LookPath("otioconvert")
	if lookErr != nil {
		return false, nil // not installed -> degrade, not a crash
	}
	out, runErr := exec.Command(bin, "-i", otioPath, "-o", outPath).CombinedOutput()
	if runErr != nil {
		return false, fmt.Errorf("otioconvert failed: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// OtioCLIAvailable reports whether the otioconvert CLI is on PATH (the OTIO Python
// package is installed). Lets the CLI explain a no-op clearly instead of silently.
func OtioCLIAvailable() bool {
	_, err := exec.LookPath("otioconvert")
	return err == nil
}
