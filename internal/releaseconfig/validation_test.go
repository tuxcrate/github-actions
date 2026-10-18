package releaseconfig

import "testing"

func TestImageName(t *testing.T) {
	if got, err := ImageName("", "Tuxcrate/Zrepl"); err != nil || got != "ghcr.io/tuxcrate/zrepl" {
		t.Fatalf("%s: %v", got, err)
	}
	for _, image := range []string{"docker.io/tuxcrate/zrepl", "ghcr.io/tuxcrate/zrepl:latest", "ghcr.io/tuxcrate/zrepl\ninjected"} {
		if _, err := ImageName(image, "tuxcrate/zrepl"); err == nil {
			t.Errorf("accepted %q", image)
		}
	}
}
