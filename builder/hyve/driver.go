package hyve

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/kr/pty"
	"github.com/mitchellh/multistep"
)

type DriverCancelCallback func(state multistep.StateBag) bool

// A driver is able to talk to bhyve/xhyve perform certain
// operations with it.
type Driver interface {
	// Stop stops a running machine, forcefully.
	Stop() error

	// Hyve executes the given command via bhyve/xhyve
	Hyve(hyveArgs ...string) error

	// TTY for COM1 serial line
	TTY() string

	// wait on shutdown of the VM with option to cancel
	WaitForShutdown(<-chan struct{}) bool

	// Driver executes the given command via qemu-img
	QemuImg(...string) error

	// Verify checks to make sure that this driver should function
	// properly. If there is any indication the driver can't function,
	// this will return an error.
	Verify() error

	// Version reads the version of bhyve/xhyve that is installed.
	Version() (string, error)
}

type HyveDriver struct {
	HyvePath    string
	QemuImgPath string

	vmCmd   *exec.Cmd
	vmEndCh <-chan int
	lock    sync.Mutex

	tty string
}

func (d *HyveDriver) Stop() error {
	d.lock.Lock()
	defer d.lock.Unlock()

	if d.vmCmd != nil {
		log.Printf("Killing Pid: %d:", d.vmCmd.Process.Pid)
		if err := d.vmCmd.Process.Kill(); err != nil {
			return err
		}
	}

	return nil
}

func (d *HyveDriver) Hyve(hyveArgs ...string) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	if d.vmCmd != nil {
		panic("Existing VM state found")
	}

	stdout_r, stdout_w := io.Pipe()
	stderr_r, stderr_w := io.Pipe()

	log.Printf("Executing %s: %#v", d.HyvePath, hyveArgs)
	cmd := exec.Command(d.HyvePath, hyveArgs...)

	//cmd.Stdout = stdout_w
	//cmd.Stderr = stderr_w

	tty, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("Error starting VM: %s", err)
	}
	defer tty.Close()

	log.Printf("Started bhyve/xhyve. Pid: %d", cmd.Process.Pid)

	r := bufio.NewReader(tty)
	firstLine, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	dev := regexp.MustCompile("COM1 connected to (/dev/ttys[0-9]+)").FindStringSubmatch(firstLine)
	if dev != nil {
		d.tty = dev[1]
		log.Printf("COM1 is connected to: %s", dev[1])
	}

	go logReader("bhyve/xhyve stdout", stdout_r)
	go logReader("bhyve/xhyve stderr", stderr_r)

	// Wait for bhyve/xhyve to complete in the background, and mark when its done
	endCh := make(chan int, 1)
	go func() {
		defer stderr_w.Close()
		defer stdout_w.Close()

		var exitCode int = 0
		if err := cmd.Wait(); err != nil {
			if exiterr, ok := err.(*exec.ExitError); ok {
				// The program has exited with an exit code != 0
				if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
					exitCode = status.ExitStatus()
				} else {
					exitCode = 254
				}
			}
		}

		endCh <- exitCode

		d.lock.Lock()
		defer d.lock.Unlock()
		d.vmCmd = nil
		d.vmEndCh = nil
	}()

	// Wait at least a couple seconds for an early fail from bhyve/xhyve so
	// we can report that.
	select {
	case exit := <-endCh:
		if exit != 0 {
			return fmt.Errorf("bhyve/xhyve failed to start. Please run with logs to get more info.")
		}
	case <-time.After(2 * time.Second):
	}

	// Setup our state so we know we are running
	d.vmCmd = cmd
	d.vmEndCh = endCh

	return nil
}

func (d *HyveDriver) TTY() string {
	return d.tty
}

func (d *HyveDriver) WaitForShutdown(cancelCh <-chan struct{}) bool {
	d.lock.Lock()
	endCh := d.vmEndCh
	d.lock.Unlock()

	if endCh == nil {
		return true
	}

	select {
	case <-endCh:
		return true
	case <-cancelCh:
		return false
	}
}

func (d *HyveDriver) QemuImg(args ...string) error {
	var stdout, stderr bytes.Buffer

	log.Printf("Executing qemu-img: %#v", args)
	cmd := exec.Command(d.QemuImgPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	stdoutString := strings.TrimSpace(stdout.String())
	stderrString := strings.TrimSpace(stderr.String())

	if _, ok := err.(*exec.ExitError); ok {
		err = fmt.Errorf("QemuImg error: %s", stderrString)
	}

	log.Printf("stdout: %s", stdoutString)
	log.Printf("stderr: %s", stderrString)

	return err
}

func (d *HyveDriver) Verify() error {
	return nil
}

func (d *HyveDriver) Version() (string, error) {
	var stdout bytes.Buffer

	cmd := exec.Command(d.HyvePath, "-v")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	versionOutput := strings.TrimSpace(stdout.String())
	log.Printf("bhyve/xhyve -v output: %s", versionOutput)
	versionRe := regexp.MustCompile("[bx]hyve: [0-9]+\\.[0-9]+\\.[0-9]+")
	matches := versionRe.Split(versionOutput, 2)
	if len(matches) == 0 {
		return "", fmt.Errorf("No version found: %s", versionOutput)
	}

	log.Printf("bhyve/xhyve version: %s", matches[0])
	return matches[0], nil
}

func logReader(name string, r io.Reader) {
	bufR := bufio.NewReader(r)
	for {
		line, err := bufR.ReadString('\n')
		if line != "" {
			line = strings.TrimRightFunc(line, unicode.IsSpace)
			log.Printf("%s: %s", name, line)
		}

		if err == io.EOF {
			break
		}
	}
}
