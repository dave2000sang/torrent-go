package main

import (
	"torrent-go/client"
	"torrent-go/torrent"
	"log"
	"time"
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

	log.Println("NUM PIECES: ", client.TorrentFile.NumPieces) // ~10000 for ubuntu image
	log.Println("NUM PEERS: ", len(client.PeerList))
	PeerConcurrentLimit := 20
	
	if TEST {
		if len(client.PeerList) > PeerConcurrentLimit {
			client.PeerList = client.PeerList[:PeerConcurrentLimit]
		}
	}

	startTime := time.Now()
	// Connect to peers and download file
	client.ConnectPeers()
	log.Println("elapsed time: ", time.Since(startTime))
}
