package main

import (
	"torrent-go/client"
	"torrent-go/torrent"
	"torrent-go/peer"
	"log"
	"time"
	"torrent-go/dht"
)
// TEST flag
const TEST = true
// UseDHT flag
const UseDHT = true
// FoundPeerMin is the minimum number of peers for DHT to fetch
const FoundPeerMin = 20
// PeerConcurrentLimit is max number of goroutines to run concurrently
const PeerConcurrentLimit = 20

func main() {
	log.Println("TEST = ", TEST)
	log.Println("UseDHT = ", UseDHT)
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

	if UseDHT {
		// Get peers from DHT (trackerless client)
		log.Println("===========================================================================")
		log.Println("Starting DHT")
		// Start the DHT node
		myDHT := dht.NewDHT()
		if err := myDHT.Start(); err != nil {
			log.Fatal("Failed to start DHT node ", err)
		}
		// Listen for peers on dht.FoundPeers channel
		select {
		case foundPeer := <-myDHT.FoundPeers:
			log.Println("FOUND PEER ", foundPeer.IP)
			newPeer, _ := peer.NewPeer(foundPeer.IP, foundPeer.Port)
			client.PeerList = append(client.PeerList, newPeer)
			if len(client.PeerList) >= FoundPeerMin {
				myDHT.Stop()
				break
			}
		case <-myDHT.DoneBootstrapping:
			log.Println("Done bootstrapping")
			// Request peers with info hash
			myDHT.TriggerGetPeers(client.TorrentFile.InfoHash)
		}
	} else {
		// Get peers list from tracker
		log.Println("Getting peers from tracker")
		client.ConnectTracker()		
	}

	log.Println("NUM PIECES: ", client.TorrentFile.NumPieces) // ~10000 for ubuntu image
	log.Println("NUM PEERS: ", len(client.PeerList))
	
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
