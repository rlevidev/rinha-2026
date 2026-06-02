package main

import (
	"bytes"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"unsafe"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/index"
	"github.com/rlevidev/rinha-2026/internal/netx"
	"golang.org/x/sys/unix"
)

const (
	bufSize   = 4096
	maxFDs    = 1024
	maxEvents = 128

	epollIn     = 0x001
	epollRdhup  = 0x2000
	schedFIFO   = 1
	workerRTPri = 10
)

type connState struct {
	buf [bufSize]byte
	pos int
}

var (
	states  []connState
	ctrlFD  int
	epollFD int

	indices [index.NPartitions]*index.IvfIndex

	hdrSep = []byte("\r\n\r\n")
	clKey  = []byte("content-length:")

	responses = [6][]byte{
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.0}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.2}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.4}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.6}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.8}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":1.0}"),
	}
	readyResp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	errResp   = []byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
)

func bindControlUDS(path string) (int, error) {
	unix.Unlink(path)
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		unix.Close(fd)
		return -1, err
	}
	unix.Chmod(path, 0o666)
	if err := unix.Listen(fd, 8); err != nil {
		unix.Close(fd)
		return -1, err
	}
	for {
		cfd, _, err := unix.Accept4(fd, unix.SOCK_CLOEXEC)
		if err == unix.EINTR {
			continue
		}
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		return cfd, nil
	}
}

func handleRequest(req []byte, bodyOff int) []byte {
	n := len(req)
	if n >= 5 && req[0] == 'P' && req[4] == ' ' {
		var r fraud.Request
		if !fraud.ParseRequest(req[bodyOff:], &r) {
			return errResp
		}
		v := fraud.Vectorize(&r)
		tag := 0
		if r.HasLastTx {
			tag |= 1
		}
		if !r.KnownMerchant {
			tag |= 2
		}
		if r.IsOnline {
			tag |= 4
		}
		if r.CardPresent {
			tag |= 8
		}
		if indices[tag] == nil {
			tag &^= 8
			if indices[tag] == nil {
				tag &^= 4
			}
		}
		cnt := indices[tag].Search(&v)
		if cnt > 5 {
			cnt = 5
		}
		return responses[cnt]
	}
	if n >= 4 && req[0] == 'G' && req[3] == ' ' {
		return readyResp
	}
	return errResp
}

type schedParam struct{ priority int32 }

func setRealtimePriority() {
	p := schedParam{priority: workerRTPri}
	unix.Syscall(unix.SYS_SCHED_SETSCHEDULER, 0, uintptr(schedFIFO), uintptr(unsafe.Pointer(&p)))
}

func closeClient(fd int) {
	unix.EpollCtl(epollFD, unix.EPOLL_CTL_DEL, fd, nil)
	unix.Close(fd)
	if fd < maxFDs {
		states[fd].pos = 0
	}
}

func contentLength(hdr []byte) int {
	i := indexFold(hdr, clKey)
	if i < 0 {
		return -1
	}
	j := i + len(clKey)
	for j < len(hdr) && (hdr[j] == ' ' || hdr[j] == '\t') {
		j++
	}
	n := 0
	for j < len(hdr) && hdr[j] >= '0' && hdr[j] <= '9' {
		n = n*10 + int(hdr[j]-'0')
		if n > bufSize {
			return bufSize + 1
		}
		j++
	}
	return n
}

func indexFold(hay, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	last := len(hay) - len(needle)
	for i := 0; i <= last; i++ {
		k := 0
		for ; k < len(needle); k++ {
			c := hay[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[k] {
				break
			}
		}
		if k == len(needle) {
			return i
		}
	}
	return -1
}

func recvNB(fd int, p []byte) (int, unix.Errno) {
	r0, _, e := unix.RawSyscall6(unix.SYS_RECVFROM, uintptr(fd),
		uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unix.MSG_DONTWAIT), 0, 0)
	return int(r0), e
}

func sendRaw(fd int, p []byte) (int, unix.Errno) {
	r0, _, e := unix.RawSyscall6(unix.SYS_SENDTO, uintptr(fd),
		uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unix.MSG_NOSIGNAL), 0, 0)
	return int(r0), e
}

func sendAll(fd int, p []byte) error {
	off := 0
	for off < len(p) {
		n, errno := sendRaw(fd, p[off:])
		if errno == unix.EINTR {
			continue
		}
		if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK {
			continue
		}
		if errno != 0 {
			return errno
		}
		off += n
	}
	return nil
}

func handleClientEvent(fd int) {
	st := &states[fd]
	if st.pos >= bufSize {
		closeClient(fd)
		return
	}
	n, errno := recvNB(fd, st.buf[st.pos:])
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK || errno == unix.EINTR {
		return
	}
	if n == 0 || errno != 0 {
		closeClient(fd)
		return
	}
	st.pos += n

	for st.pos > 0 {
		hdrEnd := bytes.Index(st.buf[:st.pos], hdrSep)
		if hdrEnd < 0 {
			return
		}
		bodyOff := hdrEnd + 4
		cl := contentLength(st.buf[:bodyOff])
		if cl < 0 {
			cl = 0
		}
		total := bodyOff + cl
		if total > bufSize {
			closeClient(fd)
			return
		}
		if st.pos < total {
			return
		}
		if err := sendAll(fd, handleRequest(st.buf[:total], bodyOff)); err != nil {
			closeClient(fd)
			return
		}
		rem := st.pos - total
		if rem > 0 {
			copy(st.buf[:rem], st.buf[total:st.pos])
		}
		st.pos = rem
	}
}

var (
	ctrlOOB   [256]byte
	fdScratch = make([]int, 0, 64)
)

func handleCtrlEvent() {
	fds, ok, err := netx.RecvFDs(ctrlFD, ctrlOOB[:], fdScratch[:0])
	if !ok || err != nil {
		return
	}
	for _, fd := range fds {
		if fd >= maxFDs {
			unix.Close(fd)
			continue
		}
		unix.SetNonblock(fd, true)
		unix.SetsockoptInt(fd, unix.SOL_TCP, unix.TCP_NODELAY, 1)
		unix.SetsockoptInt(fd, unix.SOL_TCP, unix.TCP_QUICKACK, 1)
		states[fd].pos = 0
		unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, fd,
			&unix.EpollEvent{Events: epollIn | epollRdhup, Fd: int32(fd)})
	}
}

func serverLoop() {
	runtime.LockOSThread()
	if os.Getenv("NO_FIFO") == "" {
		setRealtimePriority()
	}

	events := make([]unix.EpollEvent, maxEvents)
	for {
		n, err := unix.EpollWait(epollFD, events, 1)
		if err == unix.EINTR {
			continue
		}
		if n <= 0 {
			continue
		}
		for i := 0; i < n; i++ {
			fd := int(events[i].Fd)
			if fd == ctrlFD {
				handleCtrlEvent()
			} else {
				handleClientEvent(fd)
			}
		}
	}
}

func die(msg string) {
	os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}

func main() {
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(160 << 20)

	if len(os.Args) < 2 {
		die("usage: server <uds_path> [index_dir]")
	}
	udsPath := os.Args[1]
	indexDir := "."
	if len(os.Args) >= 3 {
		indexDir = os.Args[2]
	}

	if !index.HasAVX2() {
		die("fatal: CPU sem AVX2")
	}

	unix.Prctl(unix.PR_SET_TIMERSLACK, 1, 0, 0, 0)
	unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE)

	states = make([]connState, maxFDs)

	loaded := 0
	for i := 0; i < index.NPartitions; i++ {
		path := indexDir + "/index_p" + strconv.Itoa(i) + ".bin"
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ix, err := index.Open(path)
		if err != nil {
			die("error: failed to open index_p" + strconv.Itoa(i) + ".bin: " + err.Error())
		}
		indices[i] = ix
		loaded++
	}
	if loaded == 0 {
		die("error: no index files found in " + indexDir)
	}

	cfd, err := bindControlUDS(udsPath)
	if err != nil {
		die("error: bind_control_uds failed: " + err.Error())
	}
	ctrlFD = cfd
	unix.SetsockoptInt(ctrlFD, unix.SOL_SOCKET, unix.SO_RCVBUF, 1<<20)
	unix.SetNonblock(ctrlFD, true)

	epollFD, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		die("error: epoll_create1 failed")
	}
	netx.SetEpollBusyPoll(epollFD)
	if err := unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, ctrlFD,
		&unix.EpollEvent{Events: epollIn, Fd: int32(ctrlFD)}); err != nil {
		die("error: epoll_ctl add ctrl failed")
	}

	serverLoop()
}
