package lb

func PickBackend(seq uint64, sockets []string) string {
	return sockets[seq&1]
}
