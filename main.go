package main

import (
	"github.com/dave2000sang/torrent-go/client"
	"github.com/dave2000sang/torrent-go/torrent"
	// "fmt"
)

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	file := "example_torrents/ubuntu-20.04.1-desktop-amd64.iso.torrent"
	// Create a new Client
	client := client.NewClient()
	curTorrent, err := torrent.ReadTorrentFile(file)
	if err != nil {
		panic(err)
	}
	client.TorrentFile = curTorrent

	// Get peers list from tracker
	client.ConnectTracker()

	// Connect to Peers
	client.ConnectPeers()
}
