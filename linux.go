package fdset

import (
	"log"
	"syscall"
	"time"
)

type fdListener interface {
	FdListen(error) bool // return true if contine listening
}

type Timeout time.Time

func (self Timeout) Error() string {
	return "timeout"
}

type fdDrain int

func (self fdDrain) FdListen(err error) bool {
	if err != nil {
		if eno, ok := err.(syscall.Errno); ok && eno.Temporary() {
			return true
		}
		log.Print(err)
		return false
	}
	buf := make([]byte, 8)
	for {
		if _, err := syscall.Read(int(self), buf); err != nil {
			if eno, ok := err.(syscall.Errno); ok && eno.Temporary() {
				return true
			}
			break
		}
	}
	return false
}

type fdChan chan error

func (self fdChan) FdListen(err error) bool {
	self <- err
	return false
}

type Fdset struct {
	ctl       int
	rListener map[int]fdListener
	wListener map[int]fdListener
	rDeadline map[int]time.Time
	wDeadline map[int]time.Time
}

func (self Fdset) Close() {
	syscall.Close(self.ctl)
}

func NewFdset() *Fdset {
	self := &Fdset{
		rListener: make(map[int]fdListener),
		wListener: make(map[int]fdListener),
		rDeadline: make(map[int]time.Time),
		wDeadline: make(map[int]time.Time),
	}
	efd, e1 := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if e1 != nil {
		return nil
	}

	ctls, e2 := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if e2 != nil {
		return nil
	}
	self.ctl = ctls[0]
	self.rListener[ctls[1]] = fdDrain(ctls[1])

	go func() {
		defer func() {
			syscall.Close(ctls[1])
			self.Close()
		}()
		evs := make([]syscall.EpollEvent, 8)
		for {
			fds := make(map[int]uint32)
			for fd, _ := range self.rListener {
				fds[fd] |= syscall.EPOLLIN
			}
			for fd, _ := range self.wListener {
				fds[fd] |= syscall.EPOLLOUT
			}
			if len(fds) == 0 {
				break
			}

			have_tm := false
			var tm time.Time
			for _, t := range self.rDeadline {
				if !have_tm || t.Before(tm) {
					tm = t
				}
				have_tm = true
			}
			for _, t := range self.wDeadline {
				if !have_tm || t.Before(tm) {
					tm = t
				}
				have_tm = true
			}

			for fd, flags := range fds {
				if err := syscall.EpollCtl(efd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{
					Events: flags,
					Fd:     int32(fd),
				}); err != nil {
					if fd == int(ctls[1]) {
						// maybe ctrl closed
						return
					} else {
						log.Print(err)
						delete(fds, fd)
					}
				}
			}

			dur := time.Hour
			if have_tm {
				dur = tm.Sub(time.Now())
			}

			evLen := 0
			if dur > 0 {
				if n, err := syscall.EpollWait(efd, evs, int(dur/time.Millisecond)); err != nil {
					if eno, ok := err.(syscall.Errno); !ok || !eno.Temporary() {
						// notify fdset poll loop error
						for _, li := range self.wListener {
							li.FdListen(err)
						}
						for _, li := range self.rListener {
							li.FdListen(err)
						}
						return
					}
				} else {
					evLen = n
				}
			}

			if len(self.wDeadline) > 0 || len(self.rDeadline) > 0 {
				tm := time.Now()
				for fd, t := range self.wDeadline {
					if li, ok := self.wListener[fd]; ok {
						if t.Before(tm) {
							if !li.FdListen(Timeout(t)) {
								delete(self.wListener, fd)
							}
							delete(self.wDeadline, fd)
						}
					}
				}
				for fd, t := range self.rDeadline {
					if li, ok := self.rListener[fd]; ok {
						if t.Before(tm) {
							if !li.FdListen(Timeout(t)) {
								delete(self.rListener, fd)
							}
							delete(self.rDeadline, fd)
						}
					}
				}
			}
			for _, ev := range evs[:evLen] {
				if ev.Events&syscall.EPOLLOUT != 0 {
					if li, ok := self.wListener[int(ev.Fd)]; ok {
						if !li.FdListen(nil) {
							delete(self.wListener, int(ev.Fd))
						}
						delete(self.wDeadline, int(ev.Fd))
					}
				}
				if ev.Events&syscall.EPOLLIN != 0 {
					if li, ok := self.rListener[int(ev.Fd)]; ok {
						if !li.FdListen(nil) {
							delete(self.rListener, int(ev.Fd))
						}
						delete(self.rDeadline, int(ev.Fd))
					}
				}
			}
			for fd, _ := range fds {
				if err := syscall.EpollCtl(efd, syscall.EPOLL_CTL_DEL, fd, nil); err != nil {
					panic(err)
				}
			}
		}
	}()
	return self
}

func (self Fdset) SetReadDeadline(fd int, tm time.Time) {
	self.rDeadline[fd] = tm
	if _, err := syscall.Write(self.ctl, []byte{3}); err != nil {
		log.Print(err) // ctl closed?
	}
}

func (self Fdset) SetWriteDeadline(fd int, tm time.Time) {
	self.wDeadline[fd] = tm
	if _, err := syscall.Write(self.ctl, []byte{3}); err != nil {
		log.Print(err) // ctl closed?
	}
}

func (self Fdset) ReadWait(fd int) error {
	ch := make(chan error)
	go func() {
		self.rListener[fd] = fdChan(ch)
		syscall.Write(self.ctl, []byte{2})
	}()
	return <-ch
}

func (self Fdset) WriteWait(fd int) error {
	ch := make(chan error)
	go func() {
		self.wListener[fd] = fdChan(ch)
		syscall.Write(self.ctl, []byte{2})
	}()
	return <-ch
}
