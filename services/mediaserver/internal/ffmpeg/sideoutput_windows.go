//go:build windows

package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"sync"
)

// Windows cannot pass ExtraFiles through os/exec, so side outputs use loopback TCP.
type sideOutput struct {
	// accepted closes once ffmpeg's connection is taken, or accepting was abandoned and conn stays nil.
	accepted chan struct{}
	conn     net.Conn

	stopAccepting func()
}

// newSideOutput opens a local side output and accepts ffmpeg's connection.
func newSideOutput(cmd *exec.Cmd, fd int) (*sideOutput, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("side output listener: %w", err)
	}
	// Progress must not wait in Nagle's buffer.
	address := (&url.URL{Scheme: "tcp", Host: ln.Addr().String(), RawQuery: "tcp_nodelay=1"}).String()
	for i, arg := range cmd.Args {
		if arg == pipeURL(fd) {
			cmd.Args[i] = address
		}
	}
	l := &sideOutput{
		accepted:      make(chan struct{}),
		stopAccepting: sync.OnceFunc(func() { _ = ln.Close() }),
	}
	go func() {
		defer close(l.accepted)
		conn, err := ln.Accept()
		l.stopAccepting()
		if err == nil {
			l.conn = conn
		}
	}()
	return l, nil
}

// Read waits for ffmpeg to connect; an ffmpeg that exits without ever opening the output reads as EOF.
func (l *sideOutput) Read(p []byte) (int, error) {
	<-l.accepted
	if l.conn == nil {
		return 0, io.EOF
	}
	n, err := l.conn.Read(p)
	if errors.Is(err, net.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (l *sideOutput) Close() error {
	l.stopAccepting()
	<-l.accepted
	if l.conn == nil {
		return nil
	}
	if err := l.conn.Close(); !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// finishSideOutputs releases readers whose connection was never opened.
func finishSideOutputs(outputs []*sideOutput) {
	for _, output := range outputs {
		output.stopAccepting()
	}
}
