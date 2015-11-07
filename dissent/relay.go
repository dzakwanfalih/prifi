package main

import (
	"encoding/binary"
	"fmt"
	"encoding/hex"
	"github.com/lbarman/crypto/abstract"
	"github.com/lbarman/prifi/dcnet"
	"io"
	//"os"
	"strconv"
	"log"
	"strings"
	"net"
	"time"
	log2 "github.com/lbarman/prifi/log"
)

type Trustee struct {
	pubkey abstract.Point
}

type AnonSet struct {
	suite    abstract.Suite
	trustees []Trustee
}

// Periodic stats reporting
var begin = time.Now()
var report = begin
var numberOfReports = 0
var period, _ = time.ParseDuration("3s")
var totupcells = int64(0)
var totupbytes = int64(0)
var totdowncells = int64(0)
var totdownbytes = int64(0)

var parupcells = int64(0)
var parupbytes = int64(0)
var pardownbytes = int64(0)

func reportStatistics(payloadLength int, reportingLimit int) bool {
	now := time.Now()
	if now.After(report) {
		duration := now.Sub(begin).Seconds()

		instantUpSpeed := (float64(parupbytes)/period.Seconds())

		fmt.Printf("@ %fs; cell %f (%f) /sec, up %f (%f) B/s, down %f (%f) B/s\n",
			duration,
			 float64(totupcells)/duration, float64(parupcells)/period.Seconds(),
			 float64(totupbytes)/duration, instantUpSpeed,
			 float64(totdownbytes)/duration, float64(pardownbytes)/period.Seconds())

			// Next report time
		parupcells = 0
		parupbytes = 0
		pardownbytes = 0

		//log2.BenchmarkFloat(fmt.Sprintf("cellsize-%d-upstream-bytes", payloadLength), instantUpSpeed)

		data := struct {
		    Experiment string
		    CellSize int
		    Speed float64
		}{
		    "upstream-speed-given-cellsize",
		    payloadLength,
		    instantUpSpeed,
		}

		log2.JsonDump(data)

		report = now.Add(period)
		numberOfReports += 1

		if(reportingLimit > -1 && numberOfReports >= reportingLimit) {
			return false
		}
	}

	return true
}

var clientsConnections  []net.Conn
var trusteesConnections []net.Conn
var trusteesPublicKeys  []abstract.Point
var clientPublicKeys    []abstract.Point

func startRelay(payloadLength int, relayPort string, nClients int, nTrustees int, trusteesIp []string, reportingLimit int) {

	//the crypto parameters are static
	tg := dcnet.TestSetup(nil, suite, factory, nClients, nTrustees)
	me := tg.Relay

	//connect to the trustees
	trusteesConnections = make([]net.Conn, nTrustees)
	trusteesPublicKeys  = make([]abstract.Point, nTrustees)

	for i:= 0; i < nTrustees; i++ {
		currentTrusteeIp := strings.Replace(trusteesIp[i], "_", ":", -1) //trick for windows shell, where ":" separates args
		connectToTrustee(i, currentTrusteeIp, nClients, nTrustees, payloadLength)
	}

	//starts the client server
	lsock, err := net.Listen("tcp", relayPort)
	if err != nil {
		panic("Can't open listen socket:" + err.Error())
	}

	// Wait for all the clients to connect
	clientsConnections = make([]net.Conn, nClients)
	clientPublicKeys   = make([]abstract.Point, nClients)

	for j := 0; j < nClients; j++ {
		fmt.Printf("Waiting for %d clients (on port %s)\n", nClients-j, relayPort)

		conn, err := lsock.Accept()
		if err != nil {
			panic("Listen error:" + err.Error())
		}

		buffer := make([]byte, 512)
		_, err2 := conn.Read(buffer)
		if err2 != nil {
			panic("Read error:" + err2.Error())
		}

		ver := int(binary.BigEndian.Uint32(buffer[0:4]))

		if(ver != LLD_PROTOCOL_VERSION) {
			fmt.Println(">>>> Relay client version", ver, "!= relay version", LLD_PROTOCOL_VERSION)
			panic("fatal error")
		}

		nodeId := int(binary.BigEndian.Uint32(buffer[4:8]))
		keySize := int(binary.BigEndian.Uint32(buffer[8:12]))
		keyBytes := buffer[12:(12+keySize)]

		publicKey := suite.Point()
		err3 := publicKey.UnmarshalBinary(keyBytes)

		if err3 != nil {
			panic(">>>>  Relay : can't unmarshal client key ! " + err3.Error())
		}

		if nodeId >= 0 && nodeId < nClients {
			clientsConnections[nodeId] = conn
			clientPublicKeys[nodeId] = publicKey
		} else {
			panic("illegal node number")
		}
	}
	println("All clients and trustees connected.")

	//wait for key exchange (with the trustee) for clients
	var messageForClient []byte

	for i:=0; i<nTrustees; i++ {
		trusteePublicKeysBytes, err := trusteesPublicKeys[i].MarshalBinary()
		trusteePublicKeyLength := make([]byte, 4)
		binary.BigEndian.PutUint32(trusteePublicKeyLength, uint32(len(trusteePublicKeysBytes)))

		messageForClient = append(messageForClient, trusteePublicKeyLength...)
		messageForClient = append(messageForClient, trusteePublicKeysBytes...)

		fmt.Println(hex.Dump(trusteePublicKeysBytes))
		if err != nil{
			panic("Relay : can't marshal trustee public key n°"+strconv.Itoa(i))
		}
	}

	fmt.Println("Writing", nTrustees, "public keys to the clients")

	for i:=0; i<nClients; i++ {
		n, err := clientsConnections[i].Write(messageForClient)

		if n < len(messageForClient) || err != nil {
			fmt.Println("Could not write to client", i)
			panic("Error writing to socket:" + err.Error())
		}
	}	

	//wait for key exchange (with the clients) for trustees
	var messageForTrustees []byte

	for i:=0; i<nClients; i++ {
		clientPublicKeysBytes, err := clientPublicKeys[i].MarshalBinary()
		clientPublicKeyLength := make([]byte, 4)
		binary.BigEndian.PutUint32(clientPublicKeyLength, uint32(len(clientPublicKeysBytes)))

		messageForTrustees = append(messageForTrustees, clientPublicKeyLength...)
		messageForTrustees = append(messageForTrustees, clientPublicKeysBytes...)

		fmt.Println(hex.Dump(clientPublicKeysBytes))
		if err != nil{
			panic("Relay : can't marshal client public key n°"+strconv.Itoa(i))
		}
	}

	fmt.Println("Writing", nTrustees, "public keys to the trustees")

	for i:=0; i<nTrustees; i++ {
		n, err := trusteesConnections[i].Write(messageForTrustees)

		if n < len(messageForTrustees) || err != nil {
			fmt.Println("Could not write to trustee", i)
			panic("Error writing to socket:" + err.Error())
		}
	}	

	
	println("All crypto stuff exchanged !")

	for {
		time.Sleep(5000 * time.Millisecond)
	}


	// Create ciphertext slice bufferfers for all clients and trustees
	clientPayloadLength := me.Coder.ClientCellSize(payloadLength)
	clientsPayloadData  := make([][]byte, nClients)
	for i := 0; i < nClients; i++ {
		clientsPayloadData[i] = make([]byte, clientPayloadLength)
	}

	trusteePayloadLength := me.Coder.TrusteeCellSize(payloadLength)
	trusteesPayloadData  := make([][]byte, nTrustees)
	for i := 0; i < nTrustees; i++ {
		trusteesPayloadData[i] = make([]byte, trusteePayloadLength)
	}

	conns := make(map[int]chan<- []byte)
	downstream := make(chan dataWithConnectionId)
	nulldown := dataWithConnectionId{} // default empty downstream cell
	window := 2           // Maximum cells in-flight
	inflight := 0         // Current cells in-flight


	for {

		//TODO: change this way of breaking the loop, it's not very elegant..
		// Show periodic reports
		if(!reportStatistics(payloadLength, reportingLimit)) {
			println("Reporting limit matched; exiting the relay")
			break;
		}

		//TODO : check if it is required to send empty cell
		// See if there's any downstream data to forward.
		var downbuffer dataWithConnectionId
		select {
			case downbuffer = <-downstream: // some data to forward downstream
				//fmt.Println("Downstream data...")
				//fmt.Printf("v %d\n", len(downbuffer)-6)
			default: // nothing at the moment to forward
				downbuffer = nulldown
		}

		downstreamDataPayloadLength := len(downbuffer.data)
		downstreamData := make([]byte, 6+downstreamDataPayloadLength)
		binary.BigEndian.PutUint32(downstreamData[0:4], uint32(downbuffer.connectionId))
		binary.BigEndian.PutUint16(downstreamData[4:6], uint16(downstreamDataPayloadLength))
		copy(downstreamData[6:], downbuffer.data)

		// Broadcast the downstream data to all clients.
		for i := 0; i < nClients; i++ {
			n, err := clientsConnections[i].Write(downstreamData)

			if n != 6+downstreamDataPayloadLength {
				panic("Relay : Write to client failed, wrote "+strconv.Itoa(downstreamDataPayloadLength+6)+" where "+strconv.Itoa(n)+" was expected : " + err.Error())
			}
		}

		totdowncells++
		totdownbytes += int64(downstreamDataPayloadLength)
		pardownbytes += int64(downstreamDataPayloadLength)

		inflight++
		if inflight < window {
			continue // Get more cells in flight
		}

		me.Coder.DecodeStart(payloadLength, me.History)

		// Collect a cell ciphertext from each trustee
		for i := 0; i < nTrustees; i++ {
			
			//TODO: this looks blocking
			n, err := io.ReadFull(trusteesConnections[i], trusteesPayloadData[i])
			if n < trusteePayloadLength {
				panic("Relay : Read from trustee failed, read "+strconv.Itoa(n)+" where "+strconv.Itoa(trusteePayloadLength)+" was expected: " + err.Error())
			}

			me.Coder.DecodeTrustee(trusteesPayloadData[i])
		}

		// Collect an upstream ciphertext from each client
		for i := 0; i < nClients; i++ {

			//TODO: this looks blocking
			n, err := io.ReadFull(clientsConnections[i], clientsPayloadData[i])
			if n < clientPayloadLength {
				panic("Relay : Read from client failed, read "+strconv.Itoa(n)+" where "+strconv.Itoa(clientPayloadLength)+" was expected: " + err.Error())
			}

			me.Coder.DecodeClient(clientsPayloadData[i])
		}

		upstreamPlaintext := me.Coder.DecodeCell()
		inflight--

		totupcells++
		totupbytes += int64(payloadLength)
		parupcells++
		parupbytes += int64(payloadLength)

		// Process the decoded cell
		if upstreamPlaintext == nil {
			continue // empty or corrupt upstream cell
		}
		if len(upstreamPlaintext) != payloadLength {
			panic("DecodeCell produced wrong-size payload")
		}

		// Decode the upstream cell header (may be empty, all zeros)
		upstreamPlainTextConnId     := int(binary.BigEndian.Uint32(upstreamPlaintext[0:4]))
		upstreamPlainTextDataLength := int(binary.BigEndian.Uint16(upstreamPlaintext[4:6]))

		if upstreamPlainTextConnId == 0 {
			continue // no upstream data
		}

		//check which connection it belongs to
		//TODO: what is that ?? this is supposed to be anonymous
		conn := conns[upstreamPlainTextConnId]

		// client initiating new connection
		if conn == nil { 
			conn = relayNewConn(upstreamPlainTextConnId, downstream)
			conns[upstreamPlainTextConnId] = conn
		}

		if 6+upstreamPlainTextDataLength > payloadLength {
			log.Printf("upstream cell invalid length %d", 6+upstreamPlainTextDataLength)
			continue
		}

		conn <- upstreamPlaintext[6 : 6+upstreamPlainTextDataLength]
	}
}


func relayNewConn(connId int, downstreamData chan<- dataWithConnectionId) chan<- []byte {

	upstreamData := make(chan []byte)
	go relaySocksProxy(connId, upstreamData, downstreamData)
	return upstreamData
}

func connectToTrustee(trusteeId int, trusteeHostAddr string, nClients int, nTrustees int, payloadLength int) {
	//connect
	fmt.Println("Relay connecting to trustee", trusteeId, "on address", trusteeHostAddr)
	conn, err := net.Dial("tcp", trusteeHostAddr)
	if err != nil {
		panic("Can't connect to trustee:" + err.Error())
		//TODO : maybe code something less brutal here
	}

	//tell the trustee server our parameters
	buffer := make([]byte, 20)
	binary.BigEndian.PutUint32(buffer[0:4], uint32(LLD_PROTOCOL_VERSION))
	binary.BigEndian.PutUint32(buffer[4:8], uint32(payloadLength))
	binary.BigEndian.PutUint32(buffer[8:12], uint32(nClients))
	binary.BigEndian.PutUint32(buffer[12:16], uint32(nTrustees))
	binary.BigEndian.PutUint32(buffer[16:20], uint32(trusteeId))

	fmt.Println("Writing", LLD_PROTOCOL_VERSION, "setup is", nClients, nTrustees, "role is", trusteeId, "cellSize ", payloadLength)

	n, err := conn.Write(buffer)

	if n < 1 || err != nil {
		panic("Error writing to socket:" + err.Error())
	}

	// Now read the public key
	buffer2 := make([]byte, 1024)
	
	// Read the incoming connection into the buffer.
	reqLen, err := conn.Read(buffer2)
	if err != nil {
	    fmt.Println(">>>> Relay : error reading:", err.Error())
	}

	fmt.Println(">>>>  Relay : reading public key", reqLen)
	keySize := int(binary.BigEndian.Uint32(buffer2[4:8]))
	keyBytes := buffer2[8:(8+keySize)]


	fmt.Println(hex.Dump(keyBytes))

	publicKey := suite.Point()
	err2 := publicKey.UnmarshalBinary(keyBytes)

	if err2 != nil {
		panic(">>>>  Relay : can't unmarshal trustee key ! " + err2.Error())
	}

	fmt.Println("Trustee", trusteeId, "is connected.")
	

	//side effects
	trusteesConnections[trusteeId] = conn
	trusteesPublicKeys[trusteeId]  = publicKey
}