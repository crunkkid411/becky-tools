package audioengine

import "testing"

func TestDevice_DisplayName(t *testing.T) {
	cases := []struct {
		name   string
		device Device
		want   string
	}{
		{
			name:   "plain name is returned as-is",
			device: Device{Name: "Pro Audio Interface", ID: "iface-out"},
			want:   "Pro Audio Interface",
		},
		{
			name:   "windows-path name yields final element (pathx, OS-agnostic)",
			device: Device{Name: `C:\Windows\System32\audiodev\Speakers`, ID: "x"},
			want:   "Speakers",
		},
		{
			name:   "unix-path name yields final element",
			device: Device{Name: "/dev/snd/Speakers", ID: "x"},
			want:   "Speakers",
		},
		{
			name:   "empty name falls back to id",
			device: Device{Name: "", ID: "builtin-out"},
			want:   "builtin-out",
		},
		{
			name:   "empty name + path-like id yields id's final element",
			device: Device{Name: "", ID: `C:\dev\card0`},
			want:   "card0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.device.DisplayName(); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
