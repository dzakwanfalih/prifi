package net

import (
	"encoding/binary"
	"fmt"
	//"encoding/hex"
	"strconv"
	"io"
	"time"
	"net"
	"github.com/lbarman/crypto/abstract"
	"github.com/lbarman/prifi/config"
)

// return data, error
func ReadWithTimeOut(nodeId int, conn net.Conn, length int, timeout time.Duration, chanForTimeoutNode chan int, chanForDisconnectedNode chan int) ([]byte, bool) {
	
	//read with timeout
	timeoutChan := make(chan bool, 1)
	errorChan   := make(chan bool, 1)
	dataChan    := make(chan []byte)
	
	go func() {
	    time.Sleep(timeout)
	    timeoutChan <- true
	}()
	
	go func() {
		dataHolder := make([]byte, length)
		n, err := io.ReadFull(conn, dataHolder)

		if err != nil || n < length {
			errorChan <- true
		} else {
	    	dataChan <- dataHolder
		}
	}()

	var data []byte
	errorDuringRead := false
	select
	{
		case data = <- dataChan:

		case <-timeoutChan:
			errorDuringRead = true
			chanForTimeoutNode <- nodeId

		case <-errorChan:
			errorDuringRead = true
			chanForDisconnectedNode <- nodeId
	}

	return data, errorDuringRead
}

func ParseBaseAndPublicKeysFromConn(conn net.Conn) (abstract.Point, []abstract.Point) {
	buffer := make([]byte, 1024)
	_, err := conn.Read(buffer)
	if err != nil {
		panic("ParseBaseAndPublicKeysFromConn, couldn't read. " + err.Error())
	}

	baseSize := int(binary.BigEndian.Uint32(buffer[0:4]))
	keysSize := int(binary.BigEndian.Uint32(buffer[4+baseSize:8+baseSize]))

	baseBytes := buffer[4:4+baseSize] 
	keysBytes := buffer[8+baseSize:8+baseSize+keysSize] 

	base := config.CryptoSuite.Point()
	err2 := base.UnmarshalBinary(baseBytes)
	if err2 != nil {
		panic("ParseBaseAndPublicKeysFromConn : can't unmarshal client key ! " + err2.Error())
	}

	publicKeys := UnMarshalPublicKeyArrayFromByteArray(keysBytes, config.CryptoSuite)
	return base, publicKeys
}


func ParseBasePublicKeysAndProofFromConn(conn net.Conn) (abstract.Point, []abstract.Point, []byte) {
	buffer := make([]byte, 1024)
	_, err := conn.Read(buffer)
	if err != nil {
		panic("ParseBaseAndPublicKeysFromConn, couldn't read. " + err.Error())
	}
		
	baseSize := int(binary.BigEndian.Uint32(buffer[0:4]))
	keysSize := int(binary.BigEndian.Uint32(buffer[4+baseSize:8+baseSize]))
	proofSize := int(binary.BigEndian.Uint32(buffer[8+baseSize+keysSize:12+baseSize+keysSize]))

	baseBytes := buffer[4:4+baseSize] 
	keysBytes := buffer[8+baseSize:8+baseSize+keysSize] 
	proof := buffer[12+baseSize+keysSize:12+baseSize+keysSize+proofSize] 

	base := config.CryptoSuite.Point()
	err2 := base.UnmarshalBinary(baseBytes)
	if err2 != nil {
		panic("ParseBasePublicKeysAndProofFromConn : can't unmarshal client key ! " + err2.Error())
	}

	publicKeys := UnMarshalPublicKeyArrayFromByteArray(keysBytes, config.CryptoSuite)
	return base, publicKeys, proof
}

func WriteBaseAndPublicKeyToConn(conn net.Conn, base abstract.Point, keys []abstract.Point) {

	baseBytes, err := base.MarshalBinary()
	if err != nil {
		panic("Marshall error:" + err.Error())
	}

	publicKEysBytes := MarshalPublicKeyArrayToByteArray(keys)

	message := make([]byte, 8+len(baseBytes)+len(publicKEysBytes))

	binary.BigEndian.PutUint32(message[0:4], uint32(len(baseBytes)))
	copy(message[4:4+len(baseBytes)], baseBytes)
	binary.BigEndian.PutUint32(message[4+len(baseBytes):8+len(baseBytes)], uint32(len(publicKEysBytes)))
	copy(message[8+len(baseBytes):], publicKEysBytes)

	_, err2 := conn.Write(message)
	if err2 != nil {
		panic("Write error:" + err.Error())
	}
}

func WriteBasePublicKeysAndProofToConn(conn net.Conn, base abstract.Point, keys []abstract.Point, proof []byte) {
	baseBytes, err    := base.MarshalBinary()
	keysBytes := MarshalPublicKeyArrayToByteArray(keys)
	if err != nil {
		panic("Marshall error:" + err.Error())
	}

	//compose the message
	totalMessageLength := 12+len(baseBytes)+len(keysBytes)+len(proof)
	message := make([]byte, totalMessageLength)

	binary.BigEndian.PutUint32(message[0:4], uint32(len(baseBytes)))
	binary.BigEndian.PutUint32(message[4+len(baseBytes):8+len(baseBytes)], uint32(len(keysBytes)))
	binary.BigEndian.PutUint32(message[8+len(baseBytes)+len(keysBytes):12+len(baseBytes)+len(keysBytes)], uint32(len(proof)))

	copy(message[4:4+len(baseBytes)], baseBytes)
	copy(message[8+len(baseBytes):8+len(baseBytes)+len(keysBytes)], keysBytes)
	copy(message[12+len(baseBytes)+len(keysBytes):12+len(baseBytes)+len(keysBytes)+len(proof)], proof)

	n, err2 := conn.Write(message)
	if err2 != nil {
		panic("Write error:" + err2.Error())
	}
	if n != totalMessageLength {
		panic("WriteBasePublicKeysAndProofToConn: wrote "+strconv.Itoa(n)+", but expecetd length"+strconv.Itoa(totalMessageLength)+"." + err.Error())
	}
}

func MarshalNodeRepresentationArrayToByteArray(nodes []NodeRepresentation) []byte {
	var byteArray []byte

	msgType := make([]byte, 4)
	binary.BigEndian.PutUint32(msgType, uint32(MESSAGE_TYPE_PUBLICKEYS))
	byteArray = append(byteArray, msgType...)

	for i:=0; i<len(nodes); i++ {
		publicKeysBytes, err := nodes[i].PublicKey.MarshalBinary()
		publicKeyLength := make([]byte, 4)
		binary.BigEndian.PutUint32(publicKeyLength, uint32(len(publicKeysBytes)))

		byteArray = append(byteArray, publicKeyLength...)
		byteArray = append(byteArray, publicKeysBytes...)

		if err != nil{
			panic("can't marshal client public key n°"+strconv.Itoa(i))
		}
	}

	return byteArray
}

func BroadcastMessageToNodes(nodes []NodeRepresentation, message []byte) {
	//fmt.Println(hex.Dump(message))

	for i:=0; i<len(nodes); i++ {
		if  nodes[i].Connected {
			n, err := nodes[i].Conn.Write(message)

			//fmt.Println("[", nodes[i].Conn.LocalAddr(), " - ", nodes[i].Conn.RemoteAddr(), "]")

			if n < len(message) || err != nil {
				fmt.Println("Could not broadcast to conn", i, "gonna set it to disconnected.")
				nodes[i].Connected = false
			}
		}
	}
}

func BroadcastMessage(conns []net.Conn, message []byte) {
	for i:=0; i<len(conns); i++ {
		n, err := conns[i].Write(message)

		fmt.Println("[", conns[i].LocalAddr(), " - ", conns[i].RemoteAddr(), "]")

		if n < len(message) || err != nil {
			fmt.Println("Could not broadcast to conn", i)
			panic("Error writing to socket:" + err.Error())
		}
	}
}

func TellPublicKey(conn net.Conn, LLD_PROTOCOL_VERSION int, publicKey abstract.Point) {
	publicKeyBytes, _ := publicKey.MarshalBinary()
	keySize := len(publicKeyBytes)

	//tell the relay our public key (assume user verify through second channel)
	buffer := make([]byte, 8+keySize)
	copy(buffer[8:], publicKeyBytes)
	binary.BigEndian.PutUint32(buffer[0:4], uint32(LLD_PROTOCOL_VERSION))
	binary.BigEndian.PutUint32(buffer[4:8], uint32(keySize))

	n, err := conn.Write(buffer)

	if n < len(buffer) || err != nil {
		panic("Error writing to socket:" + err.Error())
	}
}

func MarshalPublicKeyArrayToByteArray(publicKeys []abstract.Point) []byte {
	var byteArray []byte

	msgType := make([]byte, 4)
	binary.BigEndian.PutUint32(msgType, uint32(MESSAGE_TYPE_PUBLICKEYS))
	byteArray = append(byteArray, msgType...)

	for i:=0; i<len(publicKeys); i++ {
		publicKeysBytes, err := publicKeys[i].MarshalBinary()
		publicKeyLength := make([]byte, 4)
		binary.BigEndian.PutUint32(publicKeyLength, uint32(len(publicKeysBytes)))

		byteArray = append(byteArray, publicKeyLength...)
		byteArray = append(byteArray, publicKeysBytes...)

		//fmt.Println(hex.Dump(publicKeysBytes))
		if err != nil{
			panic("can't marshal client public key n°"+strconv.Itoa(i))
		}
	}

	return byteArray
}

func UnMarshalPublicKeyArrayFromConnection(conn net.Conn, cryptoSuite abstract.Suite) []abstract.Point {
	//collect the public keys from the trustees
	buffer := make([]byte, 1024)
	_, err := conn.Read(buffer)
	if err != nil {
		panic("Read error:" + err.Error())
	}
	println("OK")

	pks := UnMarshalPublicKeyArrayFromByteArray(buffer, cryptoSuite)
	return pks
}


func UnMarshalPublicKeyArrayFromByteArray(buffer []byte, cryptoSuite abstract.Suite) []abstract.Point {

	//will hold the public keys
	var publicKeys []abstract.Point

	//safety check
	messageType := int(binary.BigEndian.Uint32(buffer[0:4]))
	if messageType != 2 {
		panic("Trying to unmarshall an array, but does not start by 2")
	}

	//parse message
	currentByte := 4
	currentPkId := 0
	for {
		if currentByte+4 > len(buffer) {
			break; //we reached the end of the array
		}

		keyLength := int(binary.BigEndian.Uint32(buffer[currentByte:currentByte+4]))

		if keyLength == 0 {
			break; //we reached the end of the array
		}

		keyBytes := buffer[currentByte+4:currentByte+4+keyLength]

		publicKey := cryptoSuite.Point()
		err2 := publicKey.UnmarshalBinary(keyBytes)
		if err2 != nil {
			panic(">>>>can't unmarshal key n°"+strconv.Itoa(currentPkId)+" ! " + err2.Error())
		}

		publicKeys = append(publicKeys, publicKey)

		currentByte += 4 + keyLength
		currentPkId += 1
	}

	return publicKeys
}