package sekeytest

import "testing"

func TestBinName(t *testing.T) {
	for goos, want := range map[string]string{"windows": "fakehelper.exe", "darwin": "fakehelper", "linux": "fakehelper"} {
		if got := binName(goos); got != want {
			t.Errorf("binName(%q) = %q, want %q", goos, got, want)
		}
	}
}
