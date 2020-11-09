package main

import (
	"torrent-go/client"
	"torrent-go/torrent"
	"log"
)

// TEST flag
const TEST = true

func main() {
	log.Println("TEST = ", TEST)
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
	
	if TEST {
		if len(client.PeerList) > 5 {
			client.PeerList = client.PeerList[:5]
		}
	}
	// Connect to peers and download file
	client.ConnectPeers()
	
}
