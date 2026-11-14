package server

import "os"

func openSource(root *os.Root, path string) (*os.File, error) { return root.Open(path) }
