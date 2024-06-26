package utils

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func ExecCommandNew(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory
	stdout, err := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	var strs = []string{}
	for {
		tmp := make([]byte, 1024)
		_, err := stdout.Read(tmp)
		strs = append(strs, string(tmp))
		if err != nil {
			break
		}
	}

	return strings.Join(strs, "###"), nil
}

func ExecCommand(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory

	var stdout, stderr []byte
	var errStdout, errStderr error
	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	err := cmd.Start()
	if err != nil {
		log.Printf("cmd.Start() failed with '%s'\n", err)
		return "", err
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		stdout, errStdout = copyAndCapture(os.Stdout, stdoutIn)
		wg.Done()
	}()

	stderr, errStderr = copyAndCapture(os.Stderr, stderrIn)

	wg.Wait()

	err = cmd.Wait()
	if err != nil {
		log.Printf("cmd.Run() failed with %s\n - out: %s, err: %s", err, string(stdout), string(stderr))
		return "", errors.New(fmt.Sprintf("%s\n%s", string(stdout), string(stderr)))
	}
	if errStdout != nil || errStderr != nil {
		return "", errors.New("failed to capture stdout or stderr\n")
	}
	outStr, errStr := string(stdout), string(stderr)
	return fmt.Sprintf("%s%s", outStr, errStr), nil
}

func copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			_, err := w.Write(d)
			if err != nil {
				return out, err
			}
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
	}
}
