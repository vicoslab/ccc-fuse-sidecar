package client

import "syscall"

func syscallClose(fd int) error {
	return syscall.Close(fd)
}
