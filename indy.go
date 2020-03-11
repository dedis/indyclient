package indyclient

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/mr-tron/base58"
	"github.com/pebbe/zmq4"
	"golang.org/x/crypto/curve25519"
)

type Pool struct {
	Validators    []Validator
	s             *zmq4.Socket // the currently open socket
	retryConn     int
	nextValidator int
	log           *log.Logger
}

type Validator struct {
	Alias   string
	VerKey  string
	Address string // client_ip:client_port
}

type Block struct {
	Txn         Txn
	TxnMetadata TxnMetadata
	Ver         int
}

type Txn struct {
	Data     DataDest
	Metadata map[string]string
	Type     protoId
}

type DataDest struct {
	Data json.RawMessage
	Dest string
}

type TxnData struct {
	Data string
	Dest string
}

type TxnMetadata struct {
	SeqNo int
	TxnId string
}

type protoId int

// Constants from the indy-node specs.
const (
	idNode   protoId = 0
	idGetTxn         = 3
)

type LedgerId int

const (
	PoolLedger   LedgerId = 0
	DomainLedger LedgerId = 0
)

type TxnNode struct {
	Alias      string
	ClientIP   string `json:"client_ip"`
	ClientPort string `json:"client_port",string`
}

// NewPool constructs a new Pool, which will follow the ledgers maintained by
// the validators in the genesis transactions read from genesis.
func NewPool(genesis io.Reader) (*Pool, error) {
	p := new(Pool)
	p.retryConn = 3
	p.log = log.New(os.Stderr, "", log.LstdFlags)

	dec := json.NewDecoder(genesis)
	for {
		var b Block
		if err := dec.Decode(&b); err == io.EOF {
			break
		}
		switch b.Txn.Type {
		case idNode:
			var n TxnNode
			err := json.Unmarshal(b.Txn.Data.Data, &n)
			if err != nil {
				log.Printf("failed to decode TxnNode: %v", err)
				continue
			}
			p.Validators = append(p.Validators, Validator{
				Alias:   n.Alias,
				VerKey:  b.Txn.Data.Dest,
				Address: net.JoinHostPort(n.ClientIP, n.ClientPort),
			})
		}
	}
	return p, nil
}

type getTxn struct {
	Operation       getTxnOp `json:"operation"`
	Identifier      string   `json:"identifier"`
	ReqId           seqNo    `json:"reqId"`
	ProtocolVersion int      `json:"protocolVersion"`
}

type getTxnOp struct {
	Type     protoId `json:"type,string"`
	Data     int     `json:"data"`
	LedgerID int     `json:"ledgerId"`
}

type Reply struct {
	Identifier string `json:"identifier"`
	Op         string `json:"op"`
	ReqId      seqNo  `json:"reqId"`
	Result     json.RawMessage
}

type Did struct {
	Method string
	Id     string
}

func DidParse(didStr string) (*Did, error) {
	u, err := url.Parse(didStr)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "did" {
		return nil, errors.New("not a DID")
	}
	if u.Opaque == "" {
		return nil, errors.New("no DID method found")
	}
	m := strings.SplitN(u.Opaque, ":", 2)
	if m[0] != "sov" {
		return nil, errors.New("not a sov DID")
	}
	if len(m) < 2 {
		return nil, errors.New("no ID found")
	}
	return &Did{
		Method: "sov",
		Id:     m[1],
	}, nil
}

const defaultIdent = "Go1ndyC1ient1111111111"

func (p *Pool) getConnection() (s *zmq4.Socket, err error) {
	if p.s != nil {
		return p.s, nil
	}

	for i := 0; i < p.retryConn; i++ {
		s, err = p.newConnection()
		if err == nil {
			p.s = s
			return s, nil
		}
		p.log.Print("failed connection, retrying:", err)
	}

	p.log.Print("failed all tries")
	s = nil
	return
}

func (p *Pool) newConnection() (*zmq4.Socket, error) {
	validator := p.Validators[p.nextValidator]
	p.nextValidator = (p.nextValidator + 1) % len(p.Validators)

	s, err := zmq4.NewSocket(zmq4.DEALER)
	if err != nil {
		return nil, err
	}

	pub, sec, err := zmq4.NewCurveKeypair()
	if err != nil {
		return nil, err
	}
	s.SetIdentity(base64.StdEncoding.EncodeToString([]byte(pub)))
	err = s.SetCurvePublickey(pub)
	if err != nil {
		return nil, err
	}
	err = s.SetCurveSecretkey(sec)
	if err != nil {
		return nil, err
	}

	vk, err := base58.Decode(validator.VerKey)
	if err != nil {
		return nil, err
	}
	srv := ed25519PublicKeyToCurve25519(ed25519.PublicKey(vk))
	err = s.SetCurveServerkey(zmq4.Z85encode(string(srv)))
	if err != nil {
		return nil, err
	}

	err = s.Connect("tcp://" + validator.Address)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Pool) GetTransaction(ledger LedgerId, seqNo int) (*Reply, error) {
	reqId := seqGetNext()
	tx := getTxn{
		Identifier: defaultIdent,
		ReqId:      reqId,
		Operation: getTxnOp{
			Type:     idGetTxn,
			Data:     seqNo,
			LedgerID: int(ledger),
		},
		ProtocolVersion: 2,
	}
	m, _ := json.Marshal(tx)

	s, err := p.getConnection()
	if err != nil {
		return nil, err
	}

	_, err = s.SendMessageDontwait(m)
	if err != nil {
		return nil, err
	}

	in, err := s.RecvMessage(0)
	if err != nil {
		return nil, err
	}
	if len(in) != 1 {
		return nil, errors.New("got wrong amount of input")
	}
	var r = new(Reply)
	err = json.Unmarshal([]byte(in[0]), r)
	if err != nil {
		return nil, err
	}
	if r.ReqId != tx.ReqId {
		return nil, errors.New("got answer to another request")
	}
	if r.Op != "REQACK" {
		return nil, fmt.Errorf("unexpected reply op: %v", r.Op)
	}
	in, err = s.RecvMessage(0)
	if err != nil {
		return nil, err
	}
	if len(in) != 1 {
		return nil, errors.New("got wrong amount of input")
	}

	r = new(Reply)
	err = json.Unmarshal([]byte(in[0]), r)
	if err != nil {
		return nil, err
	}

	return r, nil
}

type seqNo uint32

var seqNext seqNo = 1
var seqMu sync.Mutex

func seqGetNext() (s seqNo) {
	seqMu.Lock()
	s = seqNext
	seqNext++
	seqMu.Unlock()
	return
}

func (s seqNo) toBytes() []byte {
	var res [4]byte
	binary.LittleEndian.PutUint32(res[:], uint32(s))
	return res[:]
}

type timeout int32

func (t timeout) toBytes() []byte {
	var res [4]byte
	binary.LittleEndian.PutUint32(res[:], uint32(t))
	return res[:]
}

var curve25519P, _ = new(big.Int).SetString("57896044618658097711785492504343953926634992332820282019728792003956564819949", 10)

func ed25519PublicKeyToCurve25519(pk ed25519.PublicKey) []byte {
	// ed25519.PublicKey is a little endian representation of the y-coordinate,
	// with the most significant bit set based on the sign of the x-coordinate.
	bigEndianY := make([]byte, ed25519.PublicKeySize)
	for i, b := range pk {
		bigEndianY[ed25519.PublicKeySize-i-1] = b
	}
	bigEndianY[0] &= 0b0111_1111

	// The Montgomery u-coordinate is derived through the bilinear map
	//
	//     u = (1 + y) / (1 - y)
	//
	// See https://blog.filippo.io/using-ed25519-keys-for-encryption.
	y := new(big.Int).SetBytes(bigEndianY)
	denom := big.NewInt(1)
	denom.ModInverse(denom.Sub(denom, y), curve25519P) // 1 / (1 - y)
	u := y.Mul(y.Add(y, big.NewInt(1)), denom)
	u.Mod(u, curve25519P)

	out := make([]byte, curve25519.PointSize)
	uBytes := u.Bytes()
	for i, b := range uBytes {
		out[len(uBytes)-i-1] = b
	}

	return out
}
