package platform

import "fmt"

type FileOpener interface {
	Open(path string) error
}

func NewFileOpener(runner ProcessRunner) FileOpener {
	if runner == nil {
		runner = NewProcessRunner()
	}
	return newFileOpener(runner)
}

func openWithRunner(runner ProcessRunner, spec CommandSpec) error {
	cmd, err := runner.Start(spec)
	if err != nil {
		return err
	}
	if cmd == nil {
		return fmt.Errorf("file opener returned nil command")
	}
	return nil
}
