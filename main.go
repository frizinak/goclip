package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	xselTimeout   = time.Second * 2
	maxTotalBytes = 1024 * 1024 * 250
	maxBytes      = 1024 * 256
	minLen        = 0
	port          = 10345
	saveInterval  = time.Second * 30
	l             = log.New(os.Stdout, "", 0)
	xselCopy      = []string{"xsel", "-ib"}
	xselPaste     = []string{"xsel", "-ob"}
)

func xpaste() (out []byte, err error) {
	var stdout bytes.Buffer
	cmd := exec.Command(xselPaste[0], xselPaste[1:]...)

	cmd.Stdout = &stdout

	if err = cmd.Start(); err != nil {
		return
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err = <-done:
		out = stdout.Bytes()
		return
	case <-time.After(xselTimeout):
		cmd.Process.Kill()
		err = fmt.Errorf("xsel timed out after %s", xselTimeout)
	}

	return
}

func xcopy(data []byte) (err error) {
	cmd := exec.Command(xselCopy[0], xselCopy[1:]...)

	var stdin io.WriteCloser

	stdin, err = cmd.StdinPipe()

	if err != nil {
		if stdin != nil {
			defer stdin.Close()
		}

		return
	}

	if err = cmd.Start(); err != nil {
		return
	}

	if _, err = stdin.Write(data); err != nil {
		return
	}
	stdin.Close()

	done := make(chan error)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err = <-done:
		return
	case <-time.After(xselTimeout):
		err = fmt.Errorf("xsel timed out after %s", xselTimeout)
	}

	return
}

func size(data [][]byte) (bytes int) {
	for i := range data {
		bytes += len(data[i])
	}

	return
}

func daemon() error {
	var rw sync.RWMutex
	var data = [][]byte{}
	var changed bool

	user, err := user.Current()
	if err != nil {
		return err
	}
	file := user.HomeDir + "/.goclip"

	cache, err := ioutil.ReadFile(file)
	if err == nil {
		data = bytes.SplitAfter(cache, []byte{'\n'})
		if len(data) > 0 {
			data = data[:len(data)-1]
		}
	}

	daemonErr := make(chan error, 1)
	go func() {
		l, err := net.ListenTCP(
			"tcp",
			&net.TCPAddr{Port: port, IP: net.IPv4(127, 0, 0, 1)},
		)
		if err != nil {
			daemonErr <- err
			return
		}

		for {
			conn, err := l.Accept()
			if err != nil {
				continue
			}
			rw.RLock()
			for i := len(data) - 1; i >= 0; i-- {
				conn.Write(data[i])
			}
			rw.RUnlock()
			conn.Close()
		}
	}()

	items := make(chan []byte, 0)
	go func() {
		var last []byte
		if len(data) != 0 {
			last = data[len(data)-1]
		}
		for {
			time.Sleep(time.Millisecond * 250)
			new, _ := xpaste()
			if minLen > 0 {
				if strlen := utf8.RuneCount(new); strlen < minLen {
					continue
				}
			}

			if len(new) == 0 || len(new) > maxBytes {
				continue
			}

			new = append(
				bytes.Replace(
					new,
					[]byte{'\n'},
					[]byte{0},
					-1,
				),
				'\n',
			)

			if !bytes.Equal(last, new) {
				last = new
				items <- new
			}
		}
	}()

	go func() {
		for {
			time.Sleep(saveInterval)
			if !changed {
				continue
			}
			changed = false

			l.Println("Saving...")
			var buf bytes.Buffer
			rw.RLock()
			for i := range data {
				buf.Write(data[i])
			}
			rw.RUnlock()

			err := ioutil.WriteFile(file, buf.Bytes(), 0644)
			if err != nil {
				l.Println("Failed to save.")
				continue
			}

			l.Println("Saved.")
		}
	}()

	for {
		select {
		case <-daemonErr:
			return nil
		case new := <-items:
			rw.Lock()
			changed = true
			for i := range data {
				if bytes.Equal(data[i], new) {
					data = append(data[:i], data[i+1:]...)
					break
				}
			}

			data = append(data, new)
			for len(data) != 0 {
				bytes := 0
				for i := range data {
					bytes += len(data[i])
				}

				if bytes <= maxTotalBytes {
					break
				}

				data = data[1:]
			}

			rw.Unlock()
		}

	}
}

func main() {
	_maxTotalBytes := flag.Int("t", 0, "Max size in bytes")
	_maxBytes := flag.Int("m", 0, "Max size in bytes for a single entry")
	_saveInterval := flag.Int("i", 0, "Save interval in seconds")
	_minLen := flag.Int("l", 0, "Minimum length of string in clipboard")
	decode := flag.Bool("d", false, "Decode and pipe to xsel -ib")
	flag.Parse()

	if *_maxTotalBytes > 0 {
		maxTotalBytes = *_maxTotalBytes
	}

	if *_maxBytes > 0 {
		maxBytes = *_maxBytes
	}

	if *_minLen > 0 {
		minLen = *_minLen
	}

	if *_saveInterval > 0 {
		saveInterval = time.Second * time.Duration(*_saveInterval)
	}

	// Try to start the daemon.
	// blocks unless net.ListenTCP could not bind to port.
	if err := daemon(); err != nil {
		l.Fatal(err)
	}

	// Not -d: connect to daemon and write all stored data.
	if !*decode {
		conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			l.Fatal(err)
		}
		defer conn.Close()

		if _, err := io.Copy(os.Stdout, conn); err != nil {
			l.Fatal(err)
		}

		return
	}

	// If -d: pipe \0 separated byte slice to xsel.
	input, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		l.Fatal(err)
	}

	toCopy := bytes.Replace(
		bytes.Trim(
			input,
			"\n",
		),
		[]byte{0},
		[]byte{'\n'},
		-1,
	)

	if len(toCopy) != 0 {
		xcopy(toCopy)
	}
}
