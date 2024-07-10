package utils

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func ExecCommand(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory

	stdoutIn, err := cmd.StdoutPipe()

	if err != nil {
		log.Printf("Failed creating command stdoutpipe:  '%s'", err)
		return "", err
	}
	defer func(stdoutIn io.ReadCloser) {
		_ = stdoutIn.Close()
	}(stdoutIn)
	stdoutScanner := bufio.NewScanner(stdoutIn)

	stderrIn, err := cmd.StderrPipe()

	if err != nil {
		log.Printf("Failed creating command stderrpipe:  '%s'", err)
		return "", err
	}
	defer func(stderrIn io.ReadCloser) {
		_ = stderrIn.Close()
	}(stderrIn)
	stderrScanner := bufio.NewScanner(stderrIn)

	err = cmd.Start()
	if err != nil {
		log.Printf("cmd.Start() failed with '%s'\n", err)
		return "", err
	}

	var strs = []string{}

	for stdoutScanner.Scan() {
		// Do something with the line here.
		strs = append(strs, stdoutScanner.Text())
		log.Println(stdoutScanner.Text())
	}

	if stdoutScanner.Err() != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		log.Printf("stdoutScanner.Err failed with %s", stdoutScanner.Err())
		return "", errors.New(fmt.Sprintf("%s", stdoutScanner.Err()))
	}

	for stderrScanner.Scan() {
		// Do something with the line here.
		strs = append(strs, stderrScanner.Text())
		log.Println(stderrScanner.Text())
	}

	if stderrScanner.Err() != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		log.Printf("stderrScanner.Err failed with %s", stderrScanner.Err())
		return "", errors.New(fmt.Sprintf("%s", stderrScanner.Err()))
	}

	err = cmd.Wait()
	if err != nil {
		log.Printf("cmd.Run() failed with %s", err)
		return "", errors.New(fmt.Sprintf("%s", err))
	}

	return strings.Join(strs, "\n"), nil
}

func ExecCommandOld(workingDirectory string, app []string) (string, error) {
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
		return "", errors.New(fmt.Sprintf("%s%s", string(stdout), string(stderr)))
	}
	if errStdout != nil {
		return "", errors.New("failed to capture stdout " + errStdout.Error() + "\n")
	}
	if errStderr != nil {
		return "", errors.New("failed to capture stderr " + errStderr.Error() + "\n")
	}
	outStr, errStr := string(stdout), string(stderr)
	return fmt.Sprintf("%s%s", outStr, errStr), nil
}

func copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 2048, 2048)
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
