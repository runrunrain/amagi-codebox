//go:build windows

package platform

type windowsFileOpener struct {
	runner ProcessRunner
}

func newFileOpener(runner ProcessRunner) FileOpener {
	return &windowsFileOpener{runner: runner}
}

func (o *windowsFileOpener) Open(path string) error {
	return openWithRunner(o.runner, CommandSpec{
		Path:   "cmd",
		Args:   []string{"/c", "start", "", path},
		Policy: DefaultProcessPolicy(),
	})
}
