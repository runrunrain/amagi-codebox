//go:build !windows && !darwin

package platform

type genericFileOpener struct {
	runner ProcessRunner
}

func newFileOpener(runner ProcessRunner) FileOpener {
	return &genericFileOpener{runner: runner}
}

func (o *genericFileOpener) Open(path string) error {
	return openWithRunner(o.runner, CommandSpec{Path: "xdg-open", Args: []string{path}})
}
