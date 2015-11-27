package trustee

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"encoding/hex"
	"net"
	"github.com/lbarman/crypto/abstract"
	"github.com/lbarman/prifi/config"
	"time"
	prifinet "github.com/lbarman/prifi/net"
)

func StartTrusteeServer() {

	fmt.Printf("Starting Trustee Server \n")

	//async listen for incoming connections
	newConnections := make(chan net.Conn)
	go startListening(TRUSTEE_SERVER_LISTENING_PORT, newConnections)

	//active connections will be hold there
	activeConnections := make([]net.Conn, 0)

	//handler warns the handler when a connection closes
	closedConnections := make(chan int)

	for {
		select {

			// New TCP connection
			case newConn := <-newConnections:
				newConnId := len(activeConnections)
				activeConnections = append(activeConnections, newConn)

				go handleConnection(newConnId, newConn, closedConnections)

		}
	}
}


func startListening(listenport string, newConnections chan<- net.Conn) {
	fmt.Printf("Listening on port %s\n", listenport)

	lsock, err := net.Listen("tcp", listenport)

	if err != nil {
		fmt.Printf("Can't open listen socket at port %s: %s", listenport, err.Error())
		return
	}
	for {
		conn, err := lsock.Accept()
		fmt.Printf("Accepted on port %s\n", listenport)

		if err != nil {
			fmt.Printf("Accept error: %s", err.Error())
			lsock.Close()
			return
		}
		newConnections <- conn
	}
}


func initiateTrusteeState(trusteeId int, nClients int, nTrustees int, payloadLength int, conn net.Conn) *TrusteeState {
	params := new(TrusteeState)

	params.Name             = "Trustee-"+strconv.Itoa(trusteeId)
	params.TrusteeId        = trusteeId
	params.nClients         = nClients
	params.nTrustees        = nTrustees
	params.PayloadLength    = payloadLength
	params.activeConnection = conn

	//prepare the crypto parameters
	rand 	:= config.CryptoSuite.Cipher([]byte(params.Name))
	base	:= config.CryptoSuite.Point().Base()

	//generate own parameters
	params.privateKey       = config.CryptoSuite.Secret().Pick(rand)
	params.PublicKey        = config.CryptoSuite.Point().Mul(base, params.privateKey)

	//placeholders for pubkeys and secrets
	params.ClientPublicKeys = make([]abstract.Point, nClients)
	params.sharedSecrets    = make([]abstract.Point, nClients)

	//sets the cell coder, and the history
	params.CellCoder = config.Factory()

	return params
}

func handleConnection(connId int,conn net.Conn, closedConnections chan int){
	
	defer conn.Close()

	buffer := make([]byte, 1024)
	
	// Read the incoming connection into the bufferfer.
	_, err := conn.Read(buffer)
	if err != nil {
	    fmt.Println(">>>> Trustee", connId, "error reading:", err.Error())
	    return;
	}

	//Check the protocol version against ours
	version := int(binary.BigEndian.Uint32(buffer[0:4]))

	if(version != config.LLD_PROTOCOL_VERSION) {
		fmt.Println(">>>> Trustee", connId, "client version", version, "!= server version", config.LLD_PROTOCOL_VERSION)
		return;
	}

	//Extract the global parameters
	cellSize := int(binary.BigEndian.Uint32(buffer[4:8]))
	nClients := int(binary.BigEndian.Uint32(buffer[8:12]))
	nTrustees := int(binary.BigEndian.Uint32(buffer[12:16]))
	trusteeId := int(binary.BigEndian.Uint32(buffer[16:20]))
	fmt.Println(">>>> Trustee", connId, "setup is", nClients, "clients", nTrustees, "trustees, role is", trusteeId, "cellSize ", cellSize)

	
	//prepare the crypto parameters
	trusteeState := initiateTrusteeState(trusteeId, nClients, nTrustees, cellSize, conn)
	prifinet.TellPublicKey(conn, config.LLD_PROTOCOL_VERSION, trusteeState.PublicKey)

	//Read the clients' public keys from the connection
	clientsPublicKeys := prifinet.UnMarshalPublicKeyArrayFromConnection(conn, config.CryptoSuite)
	for i:=0; i<len(clientsPublicKeys); i++ {
		fmt.Println("Reading public key", i)
		trusteeState.ClientPublicKeys[i] = clientsPublicKeys[i]
		trusteeState.sharedSecrets[i] = config.CryptoSuite.Point().Mul(clientsPublicKeys[i], trusteeState.privateKey)
	}

	//check that we got all keys
	for i := 0; i<nClients; i++ {
		if trusteeState.ClientPublicKeys[i] == nil {
			panic("Trustee : didn't get the public key from client "+strconv.Itoa(i))
		}
	}

	//print all shared secrets
	for i:=0; i<nClients; i++ {
		fmt.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>")
		fmt.Println("            Client", i)
		d1, _ := trusteeState.ClientPublicKeys[i].MarshalBinary()
		d2, _ := trusteeState.sharedSecrets[i].MarshalBinary()
		fmt.Println(hex.Dump(d1))
		fmt.Println("+++")
		fmt.Println(hex.Dump(d2))
		fmt.Println("<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<")
	}

	println("All crypto stuff exchanged !")

	//do round schedulue
	base, ephPublicKeys := prifinet.ParseBaseAndPublicKeysFromConn(conn)

	//To some shuffly-stuff

	base2          := base
	ephPublicKeys2 := ephPublicKeys
	proof          := make([]byte, 50)

	//Send back the shuffle
	prifinet.WriteBasePublicKeysAndProofToConn(conn, base2, ephPublicKeys2, proof)
	fmt.Println("Shuffling done, wrote back to the relay")

	for {
		fmt.Println("all done, waiting forever")
		time.Sleep(5 * time.Second)
	}

	startTrusteeSlave(trusteeState, closedConnections)

	fmt.Println(">>>> Trustee", connId, "shutting down.")
	conn.Close()
}


func startTrusteeSlave(state *TrusteeState, closedConnections chan int) {

	incomingStream := make(chan []byte)
	go trusteeConnRead(state, incomingStream, closedConnections)

	// Just generate ciphertext cells and stream them to the server.
	exit := false
	i := 0
	for !exit {
		select {
			case readByte := <- incomingStream:
				fmt.Println("Received byte ! ", readByte)

			case connClosed := <- closedConnections:
				if connClosed == state.TrusteeId {
					fmt.Println("[safely stopping handler "+strconv.Itoa(state.TrusteeId)+"]")
					return;
				}

			default:
				// Produce a cell worth of trustee ciphertext
				tslice := state.CellCoder.TrusteeEncode(state.PayloadLength)

				// Send it to the relay
				//println("trustee slice")
				//println(hex.Dump(tslice))
				n, err := state.activeConnection.Write(tslice)

				i += 1
				fmt.Printf("["+strconv.Itoa(i)+":"+strconv.Itoa(state.TrusteeId)+"/"+strconv.Itoa(state.nClients)+","+strconv.Itoa(state.nTrustees)+"]")
				
				if n < len(tslice) || err != nil {
					//fmt.Println("can't write to socket: " + err.Error())
					//fmt.Println("\nShutting down handler", state.TrusteeId, "of conn", conn.RemoteAddr())
					fmt.Println("[error, stopping handler "+strconv.Itoa(state.TrusteeId)+"]")
					exit = true
				}

		}
	}
}


func trusteeConnRead(state *TrusteeState, incomingStream chan []byte, closedConnections chan<- int) {

	for {
		// Read up to a cell worth of data to send upstream
		buf := make([]byte, 512)
		n, err := state.activeConnection.Read(buf)

		// Connection error or EOF?
		if n == 0 {
			if err == io.EOF {
				fmt.Println("[read EOF, trustee "+strconv.Itoa(state.TrusteeId)+"]")
			} else {
				fmt.Println("[read error, trustee "+strconv.Itoa(state.TrusteeId)+" ("+err.Error()+")]")
				state.activeConnection.Close()
				return
			}
		} else {
			incomingStream <- buf
		}
	}
}