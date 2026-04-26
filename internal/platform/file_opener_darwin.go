//go:build darwin

package platform

type darwinFileOpener struct {
	runner ProcessRunner
}

func newFileOpener(runner ProcessRunner) FileOpener {
	return &darwinFileOpener{runner: runner}
}

func (o *darwinFileOpener) Open(path string) error {
	return openWithRunner(o.runner, CommandSpec{Path: "open", Args: []string{path}})
}
