package lb

func PickBackend(seq uint64, sockets []string) string {
	return sockets[seq&1]
}

func pickBackend(seq uint64, sockets []string) string {
	return PickBackend(seq, sockets)
}
