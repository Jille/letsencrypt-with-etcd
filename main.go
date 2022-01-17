package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"encoding"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"github.com/spf13/pflag"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var (
	port                 = pflag.IntP("port", "p", 8080, "Port to listen on for HTTP-01 challenges")
	email                = pflag.StringP("email", "e", "", "Your email address")
	domains              = pflag.StringSliceP("domains", "d", nil, "List of domains to request a certificate for")
	certificateDirectory = pflag.String("directory", "/letsencrypt-with-etcd/", "Directory to put certificates and private keys in")
	staging              = pflag.Bool("staging", false, "Whether to use LetsEncrypt staging")
)

func main() {
	ctx := context.Background()
	pflag.Parse()
	*certificateDirectory = strings.TrimSuffix(*certificateDirectory, "/") + "/"

	if len(*domains) == 0 {
		log.Fatal("Flag --domains (-d) is required")
	}

	log.Print("Connecting to etcd...")
	var err error
	c, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(os.Getenv("ETCD_ENDPOINTS"), ","),
		DialTimeout: 15 * time.Second,
		TLS: &tls.Config{
			InsecureSkipVerify: true,
		},
		Username: os.Getenv("ETCD_USER"),
		Password: os.Getenv("ETCD_PASSWORD"),
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer c.Close()
	log.Print("Connected.")

	accountKey := "/letsencrypt-with-etcd/account"
	fullChainKey := *certificateDirectory + (*domains)[0] + "-fullchain.pem"
	keyKey := *certificateDirectory + (*domains)[0] + "-key.pem"

	resp, err := c.Get(ctx, fullChainKey)
	if err != nil {
		log.Fatalf("Failed to fetch %s: %v", fullChainKey, err)
	}
	if len(resp.Kvs) > 0 {
		crt, err := certcrypto.ParsePEMCertificate(resp.Kvs[0].Value)
		if err != nil {
			log.Printf("Failed to parse old private key for your certificate: %v", err)
		} else {
			totalValidity := crt.NotAfter.Sub(crt.NotBefore)
			if crt.NotAfter.Add(-totalValidity / 3).After(time.Now()) {
				log.Printf("Certificate is valid until %s. Not refreshing.", crt.NotAfter)
				return
			}
		}
	}

	var myUser MyUser

	resp, err = c.Get(ctx, accountKey)
	if err != nil {
		log.Fatalf("Failed to fetch key %s from etcd: %v", accountKey, err)
	}
	if len(resp.Kvs) > 0 {
		if err := json.Unmarshal(resp.Kvs[0].Value, &myUser); err != nil {
			log.Fatalf("Failed to talk to unmarshal your letsencrypt account (from %s): %v", accountKey, err)
		}
	} else {
		log.Print("Creating new Lets Encrypt account...")
		if *email == "" {
			log.Fatalf("Flag --email (-e) is required if you don't have a Lets Encrypt account stored in %s", accountKey)
		}
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			log.Fatalf("Failed to create private key for a Lets Encrypt account: %v", err)
		}

		myUser = MyUser{
			Email: *email,
			Key:   serializablePrivateKey{privateKey},
		}
	}

	config := lego.NewConfig(myUser)
	if *staging {
		config.CADirURL = lego.LEDirectoryStaging
	}
	config.UserAgent = "https://github.com/Jille/letsencrypt-with-etcd"

	client, err := lego.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to connect to Lets Encrypt: %v", err)
	}

	if myUser.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			log.Fatalf("Failed to create Lets Encrypt account: %v", err)
		}
		myUser.Registration = reg

		b, err := json.Marshal(myUser)
		if err != nil {
			log.Fatalf("Failed to serialize your new Lets Encrypt account: %v", err)
		}
		if _, err := c.Put(ctx, accountKey, string(b)); err != nil {
			log.Fatalf("Failed to store your new Lets Encrypt account in %s: %v", accountKey, err)
		}
	}

	log.Print("Preparing for challenge...")

	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", fmt.Sprint(*port))); err != nil {
		log.Fatalf("Failed to set up HTTP-01 challenge provider: %v", err)
	}

	request := certificate.ObtainRequest{
		Domains: *domains,
		Bundle:  true,
	}

	resp, err = c.Get(ctx, keyKey)
	if err != nil {
		log.Fatalf("Failed to fetch key %s from etcd: %v", keyKey, err)
	}
	if len(resp.Kvs) > 0 {
		request.PrivateKey, err = certcrypto.ParsePEMPrivateKey(resp.Kvs[0].Value)
		if err != nil {
			log.Printf("Failed to parse old private key for your certificate: %v", err)
		}
	}

	log.Print("Requesting new certificate...")

	certificates, err := client.Certificate.Obtain(request)
	if err != nil {
		log.Fatalf("Failed to obtain new certificate from Lets Encrypt: %v", err)
	}

	if _, err := c.Txn(ctx).Then(
		clientv3.OpPut(fullChainKey, string(certificates.Certificate)),
		clientv3.OpPut(keyKey, string(certificates.PrivateKey)),
	).Commit(); err != nil {
		log.Fatalf("Failed to write new certificate: %v", err)
	}

	log.Print("Acquired new certificate!")
}

type MyUser struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration"`
	Key          serializablePrivateKey `json:"key"`
}

func (u MyUser) GetEmail() string {
	return u.Email
}

func (u MyUser) GetRegistration() *registration.Resource {
	return u.Registration
}

func (u MyUser) GetPrivateKey() crypto.PrivateKey {
	return u.Key.key
}

type serializablePrivateKey struct {
	key crypto.PrivateKey
}

func (p serializablePrivateKey) MarshalText() ([]byte, error) {
	if p.key == nil {
		return nil, nil
	}
	return certcrypto.PEMEncode(p.key), nil
}

func (p *serializablePrivateKey) UnmarshalText(text []byte) error {
	privateKey, err := certcrypto.ParsePEMPrivateKey(text)
	if err != nil {
		return err
	}
	p.key = privateKey
	return nil
}

var _ encoding.TextMarshaler = serializablePrivateKey{}
var _ encoding.TextUnmarshaler = &serializablePrivateKey{}
