package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
)

func ExecCommandOld(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("cmd.Run() failed with %s", err)
		return "", errors.New(fmt.Sprintf("%s", err))
	}

	return string(out), nil
}

func ExecCommand(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	err := cmd.Run()

	outStr, errStr := string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())
	if err != nil {
		log.Printf("cmd.Run() failed with %s\n - out: %s, err: %s", err, outStr, errStr)
		return "", errors.New(fmt.Sprintf("%s%s", outStr, errStr))
		//return "", errors.New("cmd.Run() failed with %s\n" + err.Error())
	}

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
