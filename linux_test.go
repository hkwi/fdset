package fdset

import (
	"syscall"
	"testing"
	"time"
)

func TestStop(t *testing.T) {
	fds := NewFdset()
	fds.Close()
	//	time.Sleep(time.Second)
}

func TestRead(t *testing.T) {
	hub := NewFdset()
	defer hub.Close()

	fds, e1 := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if e1 != nil {
		t.Error(e1)
	}
	ch := make(chan byte)
	go func() {
		if err := hub.ReadWait(fds[0]); err != nil {
			t.Error(err)
		} else {
			buf := make([]byte, 4)
			if n, err := syscall.Read(fds[0], buf); err != nil {
				t.Error(err)
			} else if n != 1 {
				t.Error("read too much")
			} else {
				ch <- buf[0]
			}
		}
	}()
	if n, err := syscall.Write(fds[1], []byte{0xF0}); err != nil {
		t.Error(err)
	} else if n != 1 {
		t.Error("write too much")
	}
	if r := <-ch; r != 0xF0 {
		t.Error("IO error")
	}
}

func TestRtimeout(t *testing.T) {
	hub := NewFdset()
	defer hub.Close()

	fds, e1 := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if e1 != nil {
		t.Error(e1)
	}
	hub.SetReadDeadline(fds[0], time.Now())
	ch := make(chan error)
	go func() {
		ch <- hub.ReadWait(fds[0])
	}()
	time.Sleep(time.Second)
	r := <-ch
	if _, ok := r.(Timeout); !ok {
		t.Error(r)
	}
}
