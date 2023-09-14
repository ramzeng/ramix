package ramix

import "testing"

func TestSetMode(t *testing.T) {
	SetMode(ReleaseMode)

	if mode != ReleaseMode {
		t.Errorf("SetMode(ReleaseMode) failed, mode should be %s, but got %s", ReleaseMode, mode)
	}

	SetMode(DebugMode)

	if mode != DebugMode {
		t.Errorf("SetMode(DebugMode) failed, mode should be %s, but got %s", DebugMode, mode)
	}

	defer func() {
		if err := recover(); err == nil {
			t.Errorf("SetMode(unknown) failed, should panic")
		}
	}()

	SetMode("unknown")
}
