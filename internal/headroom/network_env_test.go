package headroom

import (
	"errors"
	"testing"
)

func TestHeadroomNetworkPolicyAfterLoopPrevention(t *testing.T) {
	for _, openai := range []bool{false, true} {
		s := NewHeadroomService(newFakeRunner(), nil)
		sentinel := errors.New("core unavailable")
		called := false
		s.SetNetworkEnv(func(env []string) ([]string, error) {
			called = true
			m := envMap(env)
			key := "ANTHROPIC_TARGET_API_URL"
			if openai {
				key = "OPENAI_TARGET_API_URL"
			}
			if m[key] != "https://model.example" || m["HEADROOM_HOST"] != "127.0.0.1" {
				t.Error("network hook lost headroom target or loopback binding")
			}
			return nil, sentinel
		})
		var err error
		if openai {
			err = s.StartForOpenAI("https://model.example")
		} else {
			err = s.Start("https://model.example")
		}
		if !errors.Is(err, sentinel) || !called || s.IsRunning() {
			t.Fatal("headroom started despite failed AI proxy policy")
		}
	}
}
