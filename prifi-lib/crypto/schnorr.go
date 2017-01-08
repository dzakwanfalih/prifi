package crypto

import (
	"bytes"
	"crypto/cipher"
	"errors"

	"github.com/dedis/crypto/abstract"
)

// A basic, verifiable signature
type basicSig struct {
	C abstract.Scalar // challenge
	R abstract.Scalar // response
}

// Returns a secret that depends on a message and a point
func hashSchnorr(suite abstract.Suite, message []byte, p abstract.Point) abstract.Scalar {
	pb, _ := p.MarshalBinary()
	c := suite.Cipher(pb)
	c.Message(nil, nil, message)
	return suite.Scalar().Pick(c)
}

// SchnorrSign is a simplified implementation of Schnorr signatures that
// is based on github.com/dedis/crypto/anon/sig.go.
// The ring structure is removed and the anonymity set is reduced to
// one public key = no anonymity
// This implementation is based on the paper:
// C.P. Schnorr, Efficient identification and signatures for smart cards,
// CRYPTO '89
func SchnorrSign(suite abstract.Suite, random cipher.Stream, message []byte,
	privateKey abstract.Scalar) []byte {

	// Create random secret v and public point commitment T
	v := suite.Scalar().Pick(random)
	T := suite.Point().Mul(nil, v)

	// Create challenge c based on message and T
	c := hashSchnorr(suite, message, T)

	// Compute response r = v - x*c
	r := suite.Scalar()
	r.Mul(privateKey, c).Sub(v, r)

	// Return verifiable signature {c, r}
	// Verifier will be able to compute v = r + x*c
	// And check that hashElgamal for T and the message == c
	buf := bytes.Buffer{}
	sig := basicSig{c, r}
	suite.Write(&buf, &sig)
	return buf.Bytes()
}

// SchnorrVerify checks a signature generated by Sign. If the signature is not valid, the
// functions returns an error.
func SchnorrVerify(suite abstract.Suite, message []byte, publicKey abstract.Point,
	signatureBuffer []byte) error {

	// Decode the signature
	buf := bytes.NewBuffer(signatureBuffer)
	sig := basicSig{}
	if err := suite.Read(buf, &sig); err != nil {
		return err
	}
	r := sig.R
	c := sig.C

	// Compute base**(r + x*c) == T
	var P, T abstract.Point
	P = suite.Point()
	T = suite.Point()
	T.Add(T.Mul(nil, r), P.Mul(publicKey, c))

	// Verify that the hash based on the message and T
	// matches the challange c from the signature
	c = hashSchnorr(suite, message, T)
	if !c.Equal(sig.C) {
		return errors.New("invalid signature")
	}
	return nil
}
