package main

import (
	"torrent-go/client"
	"torrent-go/torrent"
	// "log"
)



func main() {
	file := "example_torrents/ubuntu-20.04.1-desktop-amd64.iso.torrent"
	// Create a new Client
	curTorrent, err := torrent.ReadTorrentFile(file)
	if err != nil {
		panic(err)
	}
	client, err := client.NewClient(curTorrent)
	if err != nil {
		panic(err)
	}
	defer client.DownloadFile.Close()

	// Get peers list from tracker
	client.ConnectTracker()

	// Connect to Peers
	client.ConnectPeers()
	
	// Begin downloading pieces
	client.DownloadPieces()
}
