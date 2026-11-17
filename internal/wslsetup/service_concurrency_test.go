package wslsetup

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// waitFor polls cond until true or timeout, failing the test otherwise.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within " + timeout.String())
}

// piEffectiveLogin answers the login probe with pi resolving into the user bin.
func piEffectiveLogin(*fakeWSL, string) (string, error) {
	return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/pi\n", nil
}

// TestOperationHoldsOneDistroIdentity pins the same-distro identity rule: the
// distro is resolved once at operation start and every subsequent call (and
// the per-distro lock) targets that name, even if the platform probe would
// answer differently mid-operation.
func TestOperationHoldsOneDistroIdentity(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-A"})
	f.cliFlow("pi", "1.0.0", []string{"", "/home/u/.npm-global/bin/pi"})
	base := f.user
	var mu sync.Mutex
	flipped := false
	f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
		mu.Lock()
		if !flipped {
			flipped = true
			f.setDistro("Ubuntu-B") // platform probe cache flips mid-operation
		}
		mu.Unlock()
		return base(f, script, timeout)
	}
	f.login = piEffectiveLogin

	res, err := NewService(nil).InstallTool("pi")
	if err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if !res.Success || res.Distro != "Ubuntu-A" {
		t.Fatalf("result = %+v, want success pinned to Ubuntu-A", res)
	}
	for _, c := range f.recorded() {
		if c.distro != "Ubuntu-A" {
			t.Fatalf("runner call escaped the operation distro identity: kind=%s distro=%s script=%s", c.kind, c.distro, c.script)
		}
	}
}

// TestSameDistroOperationsSerialize pins the per-distro lock: a second
// install/upgrade on the SAME distro must not reach its npm step while the
// first operation holds the lock (concurrent `npm i -g` into one
// ~/.npm-global prefix corrupts it).
func TestSameDistroOperationsSerialize(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Locked"})
	f.cliFlow("pi", "1.0.0", []string{"/home/u/.npm-global/bin/pi", "/home/u/.npm-global/bin/pi"})
	release := make(chan struct{})
	var mu sync.Mutex
	order := []string{}
	base := f.user
	f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
		if strings.Contains(script, "npm i -g") {
			mu.Lock()
			order = append(order, "npm-enter")
			mu.Unlock()
			<-release // hold the distro lock inside the npm step
			mu.Lock()
			order = append(order, "npm-exit")
			mu.Unlock()
			return "added 1 package\n", nil
		}
		return base(f, script, timeout)
	}
	f.login = piEffectiveLogin

	svc := NewService(nil)
	done1 := make(chan *InstallResult, 1)
	go func() {
		res, err := svc.UpdateTool("pi")
		if err != nil {
			t.Errorf("op1: %v", err)
		}
		done1 <- res
	}()
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) >= 1
	})

	done2 := make(chan *InstallResult, 1)
	go func() {
		res, err := svc.UpdateTool("pi")
		if err != nil {
			t.Errorf("op2: %v", err)
		}
		done2 <- res
	}()

	// While op1 sits inside npm holding the distro lock, op2 must stay blocked
	// BEFORE its own npm step.
	select {
	case res := <-done2:
		t.Fatalf("second same-distro operation completed while the lock was held: %+v", res)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	entered := len(order)
	mu.Unlock()
	if entered != 1 {
		t.Fatalf("second operation reached its npm step while the first held the lock: %v", order)
	}

	close(release)
	res1 := <-done1
	if !res1.Success {
		t.Fatalf("op1 result: %+v", res1)
	}
	select {
	case res2 := <-done2:
		if !res2.Success {
			t.Fatalf("op2 result: %+v", res2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second operation did not finish after lock release")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"npm-enter", "npm-exit", "npm-enter", "npm-exit"}
	if len(order) != len(want) {
		t.Fatalf("npm step order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("npm step order = %v, want %v", order, want)
		}
	}
}

// TestDifferentDistrosDoNotBlock pins that the lock is per-distro, NOT global:
// an operation stuck on distro D1 must not delay an operation on distro D2.
func TestDifferentDistrosDoNotBlock(t *testing.T) {
	f := &fakeWSL{distro: "D1"}
	f.cliFlow("pi", "1.0.0", []string{"/home/u/.npm-global/bin/pi", "/home/u/.npm-global/bin/pi"})
	release := make(chan struct{})
	base := f.user
	f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
		if strings.Contains(script, "npm i -g") {
			if f.currentDistro() == "D1" {
				<-release // only the D1 operation blocks
			}
			return "added 1 package\n", nil
		}
		return base(f, script, timeout)
	}
	f.login = piEffectiveLogin
	injectFake(t, f)

	svc := NewService(nil)
	done1 := make(chan *InstallResult, 1)
	go func() {
		res, err := svc.UpdateTool("pi")
		if err != nil {
			t.Errorf("op1: %v", err)
		}
		done1 <- res
	}()
	waitFor(t, 2*time.Second, func() bool {
		for _, c := range f.recorded() {
			if strings.Contains(c.script, "npm i -g") && c.distro == "D1" {
				return true
			}
		}
		return false
	})

	// Point subsequent operations at another distro; op2 must complete without
	// waiting for the blocked op1 — the locks are independent.
	f.setDistro("D2")
	done2 := make(chan *InstallResult, 1)
	go func() {
		res, err := svc.UpdateTool("pi")
		if err != nil {
			t.Errorf("op2: %v", err)
		}
		done2 <- res
	}()

	select {
	case res2 := <-done2:
		if !res2.Success || res2.Distro != "D2" {
			t.Fatalf("cross-distro operation result: %+v", res2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operation on a different distro was blocked by a global lock")
	}

	close(release)
	if res1 := <-done1; !res1.Success || res1.Distro != "D1" {
		t.Fatalf("blocked operation result after release: %+v", res1)
	}
}
