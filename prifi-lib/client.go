package prifi_lib

/**
 * PriFi Client
 * ************
 * This regroups the behavior of the PriFi client.
 * Needs to be instantiated via the PriFiProtocol in prifi.go
 * Then, this file simple handle the answer to the different message kind :
 *
 * - ALL_ALL_SHUTDOWN - kill this client
 * - ALL_ALL_PARAMETERS (specialized into ALL_CLI_PARAMETERS) - used to initialize the client over the network / overwrite its configuration
 * - REL_CLI_TELL_TRUSTEES_PK - the trustee's identities. We react by sending our identity + ephemeral identity
 * - REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG - the shuffle from the trustees. We do some check, if they pass, we can communicate. We send the first round to the relay.
 * - REL_CLI_DOWNSTREAM_DATA - the data from the relay, for one round. We react by finishing the round (sending our data to the relay)
 *
 * local functions :
 *
 * ProcessDownStreamData() <- is called by Received_REL_CLI_DOWNSTREAM_DATA; it handles the raw data received
 * SendUpstreamData() <- it is called at the end of ProcessDownStreamData(). Hence, after getting some data down, we send some data up.
 *
 * TODO : traffic need to be encrypted
 * TODO : we need to test / sort out the downstream traffic data that is not for us
 * TODO : integrate a VPN / SOCKS somewhere, for now this client has nothing to say ! (except latency-test messages)
 * TODO : More fine-grained locking
 */

import (
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/dedis/cothority/log"
	"github.com/dedis/crypto/abstract"
	"github.com/lbarman/prifi/prifi-lib/config"
	"github.com/lbarman/prifi/prifi-lib/crypto"
	"github.com/lbarman/prifi/prifi-lib/dcnet"
	prifilog "github.com/lbarman/prifi/prifi-lib/log"

	"github.com/dedis/crypto/random"
	socks "github.com/lbarman/prifi/prifi-socks"
)

// Possible states of the clients. This restrict the kind of messages they can receive at a given point in time.
const (
	CLIENT_STATE_BEFORE_INIT int16 = iota
	CLIENT_STATE_INITIALIZING
	CLIENT_STATE_EPH_KEYS_SENT
	CLIENT_STATE_READY
	CLIENT_STATE_SHUTDOWN
)

// ClientState contains the mutable state of the client.
type ClientState struct {
	mutex                     *sync.RWMutex
	CellCoder                 dcnet.CellCoder
	currentState              int16
	DataForDCNet              chan []byte //Data to the relay : VPN / SOCKS should put data there !
	DataFromDCNet             chan []byte //Data from the relay : VPN / SOCKS should read data from there !
	DataOutputEnabled         bool        //if FALSE, nothing will be written to DataFromDCNet
	ephemeralPrivateKey       abstract.Scalar
	EphemeralPublicKey        abstract.Point
	ID                        int
	LatencyTest               bool
	MySlot                    int
	Name                      string
	nClients                  int
	nTrustees                 int
	PayloadLength             int
	privateKey                abstract.Scalar
	PublicKey                 abstract.Point
	sharedSecrets             []abstract.Point
	TrusteePublicKey          []abstract.Point
	UsablePayloadLength       int
	UseSocksProxy             bool
	UseUDP                    bool
	MessageHistory            abstract.Cipher
	StartStopReceiveBroadcast chan bool
	statistics                *prifilog.LatencyStatistics

	//concurrent stuff
	RoundNo           int32
	BufferedRoundData map[int32]REL_CLI_DOWNSTREAM_DATA
}

// NewClientState is used to initialize the state of the client. Must be called before anything else.
func NewClientState(clientID int, nTrustees int, nClients int, payloadLength int, latencyTest bool, useUDP bool, dataOutputEnabled bool, dataForDCNet chan []byte, dataFromDCNet chan []byte) *ClientState {

	//set the defaults
	params := new(ClientState)
	params.ID = clientID
	params.Name = "Client-" + strconv.Itoa(clientID)
	params.CellCoder = config.Factory()
	params.DataForDCNet = dataForDCNet
	params.DataFromDCNet = dataFromDCNet
	params.DataOutputEnabled = dataOutputEnabled
	params.LatencyTest = latencyTest
	//params.MessageHistory =
	params.MySlot = -1
	params.nClients = nClients
	params.nTrustees = nTrustees
	params.PayloadLength = payloadLength
	params.UsablePayloadLength = params.CellCoder.ClientCellSize(payloadLength)
	params.UseSocksProxy = false //deprecated
	params.UseUDP = useUDP
	params.RoundNo = int32(0)
	params.BufferedRoundData = make(map[int32]REL_CLI_DOWNSTREAM_DATA)
	params.StartStopReceiveBroadcast = make(chan bool)
	params.statistics = prifilog.NewLatencyStatistics()
	params.mutex = &sync.RWMutex{}

	//prepare the crypto parameters
	rand := config.CryptoSuite.Cipher([]byte(params.Name))
	base := config.CryptoSuite.Point().Base()

	//generate own parameters
	params.privateKey = config.CryptoSuite.Scalar().Pick(rand)                 //NO, this should be kept by SDA
	params.PublicKey = config.CryptoSuite.Point().Mul(base, params.privateKey) //NO, this should be kept by SDA

	//placeholders for pubkeys and secrets
	params.TrusteePublicKey = make([]abstract.Point, nTrustees)
	params.sharedSecrets = make([]abstract.Point, nTrustees)

	//sets the new state
	params.currentState = CLIENT_STATE_INITIALIZING

	return params
}

// Received_ALL_CLI_SHUTDOWN handles ALL_CLI_SHUTDOWN messages.
// When we receive this message, we should clean up resources.
func (p *PriFiLibInstance) Received_ALL_CLI_SHUTDOWN(msg ALL_ALL_SHUTDOWN) error {
	log.Lvl1("Client " + strconv.Itoa(p.clientState.ID) + " : Received a SHUTDOWN message. ")

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	p.clientState.currentState = CLIENT_STATE_SHUTDOWN

	return nil
}

// Received_ALL_CLI_PARAMETERS handles ALL_CLI_PARAMETERS messages.
// It uses the message's parameters to initialize the client.
func (p *PriFiLibInstance) Received_ALL_CLI_PARAMETERS(msg ALL_ALL_PARAMETERS) error {

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	//this can only happens in the state RELAY_STATE_BEFORE_INIT
	if p.clientState.currentState != CLIENT_STATE_BEFORE_INIT && !msg.ForceParams {
		log.Lvl1("Client " + strconv.Itoa(p.clientState.ID) + " : Received a ALL_ALL_PARAMETERS, but not in state CLIENT_STATE_BEFORE_INIT, ignoring. ")
		return nil
	} else if p.clientState.currentState != CLIENT_STATE_BEFORE_INIT && msg.ForceParams {
		log.Lvl2("Client " + strconv.Itoa(p.clientState.ID) + " : Received a ALL_ALL_PARAMETERS && ForceParams = true, processing. ")
	} else {
		log.Lvl3("Client : received ALL_ALL_PARAMETERS")
	}

	//if by chance we had a broadcast-listener goroutine, kill it
	if p.clientState.StartStopReceiveBroadcast != nil {
		p.clientState.StartStopReceiveBroadcast <- false
	}

	p.clientState = *NewClientState(msg.NextFreeClientID, msg.NTrustees, msg.NClients, msg.UpCellSize, msg.DoLatencyTests, msg.UseUDP, msg.ClientDataOutputEnabled, make(chan []byte), make(chan []byte))

	//start the broadcast-listener goroutine
	log.Lvl2("Client " + strconv.Itoa(p.clientState.ID) + " : starting the broadcast-listener goroutine")
	go p.messageSender.ClientSubscribeToBroadcast(p.clientState.Name, p, p.clientState.StartStopReceiveBroadcast)

	//after receiving this message, we are done with the state CLIENT_STATE_BEFORE_INIT, and are ready for initializing
	p.clientState.currentState = CLIENT_STATE_INITIALIZING

	log.Lvlf5("%+v\n", p.clientState)
	log.Lvl2("Client " + strconv.Itoa(p.clientState.ID) + " has been initialized by message. ")

	return nil
}

/*
Received_REL_CLI_DOWNSTREAM_DATA handles REL_CLI_DOWNSTREAM_DATA messages which are part of PriFi's main loop.
This is what happens in one round, for this client. We receive some downstream data.
It should be encrypted, and we should test if this data is for us or not; if so, push it into the SOCKS/VPN chanel.
For now, we do nothing with the downstream data.
Once we received some data from the relay, we need to reply with a DC-net cell (that will get combined with other client's cell to produce some plaintext).
If we're lucky (if this is our slot), we are allowed to embed some message (which will be the output produced by the relay). Either we send something from the
SOCKS/VPN data, or if we're running latency tests, we send a "ping" message to compute the latency. If we have nothing to say, we send 0's.
*/
func (p *PriFiLibInstance) Received_REL_CLI_DOWNSTREAM_DATA(msg REL_CLI_DOWNSTREAM_DATA) error {

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	//this can only happens in the state TRUSTEE_STATE_SHUFFLE_DONE
	if p.clientState.currentState != CLIENT_STATE_READY {
		e := "Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_DOWNSTREAM_DATA, but not in state CLIENT_STATE_READY, in state " + strconv.Itoa(int(p.clientState.currentState))
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)))

	//check if it is in-order
	if msg.RoundID == p.clientState.RoundNo {
		//process downstream data

		return p.ProcessDownStreamData(msg)
	} else if msg.RoundID < p.clientState.RoundNo {
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)) + " but we are in round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", discarding.")
	} else if msg.RoundID < p.clientState.RoundNo {
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)) + " but we are in round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", buffering.")

		p.clientState.BufferedRoundData[msg.RoundID] = msg
	}

	return nil
}

/*
Received_REL_CLI_UDP_DOWNSTREAM_DATA handles REL_CLI_UDP_DOWNSTREAM_DATA messages which are part of PriFi's main loop.
This is what happens in one round, for this client.
We receive some downstream data. It should be encrypted, and we should test if this data is for us or not; is so, push it into the SOCKS/VPN chanel.
For now, we do nothing with the downstream data.
Once we received some data from the relay, we need to reply with a DC-net cell (that will get combined with other client's cell to produce some plaintext).
If we're lucky (if this is our slot), we are allowed to embed some message (which will be the output produced by the relay). Either we send something from the
SOCKS/VPN data, or if we're running latency tests, we send a "ping" message to compute the latency. If we have nothing to say, we send 0's.
*/
func (p *PriFiLibInstance) Received_REL_CLI_UDP_DOWNSTREAM_DATA(msg REL_CLI_DOWNSTREAM_DATA) error {

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	/*
		if msg.RoundId == 3 && p.clientState.Id == 1 {
			log.Error("Client " + strconv.Itoa(p.clientState.Id) + " : simulating loss, dropping UDP message for round 3.")
			return nil
		}
	*/

	//this can only happens in the state TRUSTEE_STATE_SHUFFLE_DONE
	if p.clientState.currentState != CLIENT_STATE_READY {
		e := "Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_UDP_DOWNSTREAM_DATA, but not in state CLIENT_STATE_READY, in state " + strconv.Itoa(int(p.clientState.currentState))
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_UDP_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)))

	//check if it is in-order
	if msg.RoundID == p.clientState.RoundNo {
		//process downstream data

		return p.ProcessDownStreamData(msg)
	} else if msg.RoundID < p.clientState.RoundNo {
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_UDP_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)) + " but we are in round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", discarding.")
	} else if msg.RoundID < p.clientState.RoundNo {
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_UDP_DOWNSTREAM_DATA for round " + strconv.Itoa(int(msg.RoundID)) + " but we are in round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", buffering.")

		p.clientState.BufferedRoundData[msg.RoundID] = msg
	}

	return nil
}

/*
ProcessDownStreamData handles the downstream data. After determining if the data is for us (this is not done yet), we test if it's a
latency-test message, test if the resync flag is on (which triggers a re-setup).
When this function ends, it calls SendUpstreamData() which continues the communication loop.
*/
func (p *PriFiLibInstance) ProcessDownStreamData(msg REL_CLI_DOWNSTREAM_DATA) error {

	log.Error("Here 0")
	/*
	 * HANDLE THE DOWNSTREAM DATA
	 */

	//if it's just one byte, no data
	if len(msg.Data) > 1 {

		//pass the data to the VPN/SOCKS5 proxy, if enabled
		if p.clientState.DataOutputEnabled {
			log.Error("Ninja 0 - len is", len(msg.Data))
			p.clientState.DataFromDCNet <- msg.Data
			log.Error("Ninja 1")
		}
		//test if it is the answer from our ping (for latency test)
		if p.clientState.LatencyTest && len(msg.Data) > 2 {

			pattern := int(binary.BigEndian.Uint16(msg.Data[0:2]))
			if pattern == 43690 {
				//1010101010101010
				clientID := int(binary.BigEndian.Uint16(msg.Data[2:4]))
				if clientID == p.clientState.ID {
					timestamp := int64(binary.BigEndian.Uint64(msg.Data[4:12]))
					diff := MsTimeStamp() - timestamp

					p.clientState.statistics.AddLatency(diff)
					p.clientState.statistics.Report()
				}
			}
		}
	}

	//if the flag "Resync" is on, we cannot write data up, but need to resend the keys instead
	if msg.FlagResync == true {

		log.Lvl1("Client " + strconv.Itoa(p.clientState.ID) + " : Relay wants to resync, going to state CLIENT_STATE_INITIALIZING ")
		p.clientState.currentState = CLIENT_STATE_INITIALIZING

		//TODO : regenerate ephemeral keys ?

		return nil
	}

	log.Error("Here")

	//send upstream data for next round
	return p.SendUpstreamData()
}

/*
SendUpstreamData determines if it's our round, embeds data (maybe latency-test message) in the payload if we can,
creates the DC-net cipher and sends it to the relay.
*/
func (p *PriFiLibInstance) SendUpstreamData() error {
	//TODO: maybe make this into a method func (p *PrifiProtocol) isMySlot() bool {}
	//write the next upstream slice. First, determine if we can embed payload this round
	currentRound := p.clientState.RoundNo % int32(p.clientState.nClients)
	isMySlot := false
	if currentRound == int32(p.clientState.MySlot) {
		isMySlot = true
	}

	var upstreamCellContent []byte

	//if we can...
	if isMySlot {
		select {

		//either select data from the data we have to send, if any
		case myData := <-p.clientState.DataForDCNet:
			upstreamCellContent = myData

		//or, if we have nothing to send, and we are doing Latency tests, embed a pre-crafted message that we will recognize later on
		default:
			emptyData := socks.NewSocksPacket(socks.DummyData, 0, 0, uint16(p.clientState.PayloadLength), make([]byte, 0))
			upstreamCellContent = emptyData.ToBytes()

			if p.clientState.LatencyTest {

				if p.clientState.PayloadLength < 12 {
					panic("Trying to do a Latency test, but payload is smaller than 10 bytes.")
				}

				buffer := make([]byte, p.clientState.PayloadLength)
				pattern := uint16(43690)  //1010101010101010
				currTime := MsTimeStamp() //timestamp in Ms

				binary.BigEndian.PutUint16(buffer[0:2], pattern)
				binary.BigEndian.PutUint16(buffer[2:4], uint16(p.clientState.ID))
				binary.BigEndian.PutUint64(buffer[4:12], uint64(currTime))

				upstreamCellContent = buffer
			}
		}
	}

	log.Error("Here2")

	//produce the next upstream cell
	upstreamCell := p.clientState.CellCoder.ClientEncode(upstreamCellContent, p.clientState.PayloadLength, p.clientState.MessageHistory)

	//send the data to the relay
	toSend := &CLI_REL_UPSTREAM_DATA{p.clientState.ID, p.clientState.RoundNo, upstreamCell}
	err := p.messageSender.SendToRelay(toSend)
	if err != nil {
		e := "Could not send CLI_REL_UPSTREAM_DATA, for round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", error is " + err.Error()
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : sent CLI_REL_UPSTREAM_DATA for round " + strconv.Itoa(int(p.clientState.RoundNo)))

	//clean old buffered messages
	delete(p.clientState.BufferedRoundData, int32(p.clientState.RoundNo-1))

	//one round just passed
	p.clientState.RoundNo++

	//now we will be expecting next message. Except if we already received and buffered it !
	if msg, hasAMessage := p.clientState.BufferedRoundData[int32(p.clientState.RoundNo)]; hasAMessage {
		p.Received_REL_CLI_DOWNSTREAM_DATA(msg)
	}

	return nil
}

/*
Received_REL_CLI_TELL_TRUSTEES_PK handles REL_CLI_TELL_TRUSTEES_PK messages. These are sent when we connect.
The relay sends us a pack of public key which correspond to the set of pre-agreed trustees.
Of course, there should be check on those public keys (each client need to trust one), but for now we assume those public keys belong indeed to the trustees,
and that clients have agreed on the set of trustees.
Once we receive this message, we need to reply with our Public Key (Used to derive DC-net secrets), and our Ephemeral Public Key (used for the Shuffle protocol)
*/
func (p *PriFiLibInstance) Received_REL_CLI_TELL_TRUSTEES_PK(msg REL_CLI_TELL_TRUSTEES_PK) error {

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	//this can only happens in the state CLIENT_STATE_INITIALIZING
	if p.clientState.currentState != CLIENT_STATE_INITIALIZING {
		e := "Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_TELL_TRUSTEES_PK, but not in state CLIENT_STATE_INITIALIZING, in state " + strconv.Itoa(int(p.clientState.currentState))
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_TELL_TRUSTEES_PK")

	//sanity check
	if len(msg.Pks) < 1 {
		e := "Client " + strconv.Itoa(p.clientState.ID) + " : len(msg.Pks) must be >= 1"
		log.Error(e)
		return errors.New(e)
	}

	//TODO: this is redundant, we already know the number of trustees
	//first, collect the public keys from the trustees, and derive the secrets
	p.clientState.nTrustees = len(msg.Pks)

	p.clientState.TrusteePublicKey = make([]abstract.Point, p.clientState.nTrustees)
	p.clientState.sharedSecrets = make([]abstract.Point, p.clientState.nTrustees)

	for i := 0; i < len(msg.Pks); i++ {
		p.clientState.TrusteePublicKey[i] = msg.Pks[i]
		p.clientState.sharedSecrets[i] = config.CryptoSuite.Point().Mul(msg.Pks[i], p.clientState.privateKey)
	}

	//then, generate our ephemeral keys (used for shuffling)
	p.clientState.generateEphemeralKeys()

	//send the keys to the relay
	toSend := &CLI_REL_TELL_PK_AND_EPH_PK{p.clientState.PublicKey, p.clientState.EphemeralPublicKey}
	err := p.messageSender.SendToRelay(toSend)
	if err != nil {
		e := "Could not send CLI_REL_TELL_PK_AND_EPH_PK, error is " + err.Error()
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : sent CLI_REL_TELL_PK_AND_EPH_PK")

	//change state
	p.clientState.currentState = CLIENT_STATE_EPH_KEYS_SENT

	return nil
}

/*
Received_REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG handles REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG messages.
These are sent after the Shuffle protocol has been done by the Trustees and the Relay.
The relay is sending us the result, so we should check that the protocol went well :
1) each trustee announced must have signed the shuffle
2) we need to locate which is our slot <-- THIS IS BUGGY NOW
When this is done, we are ready to communicate !
As the client should send the first data, we do so; to keep this function simple, the first data is blank
(the message has no content / this is a wasted message). The actual embedding of data happens only in the
"round function", that is Received_REL_CLI_DOWNSTREAM_DATA().
*/
func (p *PriFiLibInstance) Received_REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG(msg REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG) error {

	p.clientState.mutex.Lock()
	defer p.clientState.mutex.Unlock()

	//this can only happens in the state CLIENT_STATE_EPH_KEYS_SENT
	if p.clientState.currentState != CLIENT_STATE_EPH_KEYS_SENT {
		e := "Client " + strconv.Itoa(p.clientState.ID) + " : Received a REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG, but not in state CLIENT_STATE_EPH_KEYS_SENT, in state " + strconv.Itoa(int(p.clientState.currentState))
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : REL_CLI_TELL_EPH_PKS_AND_TRUSTEES_SIG") //TODO: this should be client

	//only at this moment we really learn the number of clients
	p.clientState.nClients = len(msg.EphPks)

	//verify the signature
	G := msg.Base
	ephPubKeys := msg.EphPks
	signatures := msg.TrusteesSigs

	G_bytes, _ := G.MarshalBinary()
	var M []byte
	M = append(M, G_bytes...)
	for k := 0; k < len(ephPubKeys); k++ {
		pkBytes, _ := ephPubKeys[k].MarshalBinary()
		M = append(M, pkBytes...)
	}

	for j := 0; j < p.clientState.nTrustees; j++ {
		err := crypto.SchnorrVerify(config.CryptoSuite, M, p.clientState.TrusteePublicKey[j], signatures[j])

		if err != nil {
			e := "Client " + strconv.Itoa(p.clientState.ID) + " : signature from trustee " + strconv.Itoa(j) + " is invalid "
			log.Error(e)
			return errors.New(e)
		}
	}

	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + "; all signatures Ok")

	//now, using the ephemeral keys received (the output of the neff shuffle), identify our slot
	myPrivKey := p.clientState.ephemeralPrivateKey
	base := config.CryptoSuite.Point().Base()
	newBaseFromTrusteesG := config.CryptoSuite.Point().Mul(base, G)
	ephPubInNewBase := config.CryptoSuite.Point().Mul(newBaseFromTrusteesG, myPrivKey)
	mySlot := -1

	for j := 0; j < len(ephPubKeys); j++ {
		if ephPubKeys[j].Equal(ephPubInNewBase) {
			mySlot = j
		}
	}

	if mySlot == -1 {
		e := "Client " + strconv.Itoa(p.clientState.ID) + "; Can't recognize our slot !"
		log.Error(e)

		mySlot = p.clientState.ID
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + "; Our self-assigned slot is " + strconv.Itoa(mySlot) + " out of " + strconv.Itoa(len(ephPubKeys)) + " slots")

		//return errors.New(e)
	} else {
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + "; Our slot is " + strconv.Itoa(mySlot) + " out of " + strconv.Itoa(len(ephPubKeys)) + " slots")
	}

	//prepare for commmunication
	p.clientState.MySlot = mySlot
	p.clientState.RoundNo = int32(0)
	p.clientState.BufferedRoundData = make(map[int32]REL_CLI_DOWNSTREAM_DATA)

	//if by chance we had a broadcast-listener goroutine, kill it
	if p.clientState.UseUDP {
		if p.clientState.StartStopReceiveBroadcast == nil {
			e := "Client " + strconv.Itoa(p.clientState.ID) + " wish to start listening with UDP, but doesn't have the appropriate helper."
			log.Error(e)
			return errors.New(e)
		}
		p.clientState.StartStopReceiveBroadcast <- true
		log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " indicated the udp-helper to start listening.")
	}

	//change state
	p.clientState.currentState = CLIENT_STATE_READY
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " is ready to communicate.")

	//produce a blank cell (we could embed data, but let's keep the code simple, one wasted message is not much)
	upstreamCell := p.clientState.CellCoder.ClientEncode(make([]byte, 0), p.clientState.PayloadLength, p.clientState.MessageHistory)

	//send the data to the relay
	toSend := &CLI_REL_UPSTREAM_DATA{p.clientState.ID, p.clientState.RoundNo, upstreamCell}
	err := p.messageSender.SendToRelay(toSend)
	if err != nil {
		e := "Could not send CLI_REL_UPSTREAM_DATA, for round " + strconv.Itoa(int(p.clientState.RoundNo)) + ", error is " + err.Error()
		log.Error(e)
		return errors.New(e)
	}
	log.Lvl3("Client " + strconv.Itoa(p.clientState.ID) + " : sent CLI_REL_UPSTREAM_DATA for round " + strconv.Itoa(int(p.clientState.RoundNo)))

	p.clientState.RoundNo++

	return nil
}

// generateEphemeralKeys is an auxiliary function used by Received_REL_CLI_TELL_TRUSTEES_PK
func (clientState *ClientState) generateEphemeralKeys() {

	//prepare the crypto parameters
	base := config.CryptoSuite.Point().Base()

	//generate ephemeral keys
	Epriv := config.CryptoSuite.Scalar().Pick(random.Stream)
	Epub := config.CryptoSuite.Point().Mul(base, Epriv)

	clientState.EphemeralPublicKey = Epub
	clientState.ephemeralPrivateKey = Epriv

}

// MsTimeStamp returns the current timestamp, in milliseconds.
func MsTimeStamp() int64 {
	//http://stackoverflow.com/questions/24122821/go-golang-time-now-unixnano-convert-to-milliseconds
	return time.Now().UnixNano() / int64(time.Millisecond)
}
