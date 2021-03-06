package config

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"regexp"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/daknob/eldim/internal/gcs"
	"github.com/daknob/eldim/internal/s3"

	"github.com/daknob/eldim/internal/backend"

	"github.com/daknob/eldim/internal/swift"
	"gopkg.in/yaml.v2"
)

/*
Config is the data structure outlying the configuration file of eldim
*/
type Config struct {
	/* Web Server Settings */
	ListenPort   int   `yaml:"listenport"`
	ServerTokens bool  `yaml:"servertokens"`
	MaxUploadRAM int64 `yaml:"maxuploadram"`

	/* TLS Settings */
	TLSChainPath string `yaml:"tlschain"`
	TLSKeyPath   string `yaml:"tlskey"`

	/* Backend Server */
	SwiftBackends []swift.BackendConfig `yaml:"swiftbackends"`
	GCSBackends   []gcs.BackendConfig   `yaml:"gcsbackends"`
	S3Backends    []s3.BackendConfig    `yaml:"s3backends"`

	/* Clients */
	ClientFile string `yaml:"clientfile"`

	/* Encryption */
	EncryptionKey string `yaml:"encryptionkey"` // Deprecated in eldim v0.6.0
	Encryption    struct {
		AgeID  []string `yaml:"age-id"`
		AgeSSH []string `yaml:"age-ssh"`
	} `yaml:"encryption"`

	/* Prometheus Metrics */
	PrometheusEnabled  bool   `yaml:"prometheusenabled"`
	PrometheusAuthUser string `yaml:"prometheusauthuser"`
	PrometheusAuthPass string `yaml:"prometheusauthpass"`
}

/*
Validate validates the eldim configuration file and returns the
first error that occured
*/
func (conf *Config) Validate() error {
	/* Validate Listening Port */
	if conf.ListenPort < 0 {
		return fmt.Errorf("TCP Listening Port must be positive number")
	}
	if conf.ListenPort > 65535 {
		return fmt.Errorf("TCP Listening Port must be below 65535")
	}

	/* Validate TLS Chain File */
	if conf.TLSChainPath == "" {
		return fmt.Errorf("TLS Chain File is required. eldim works only with HTTPS")
	}
	f, err := os.Open(conf.TLSChainPath)
	if err != nil {
		return fmt.Errorf("Failed to open TLS Chain File: %v", err)
	}
	err = f.Close()
	if err != nil {
		return fmt.Errorf("Failed to close TLS Chain File: %v", err)
	}

	/* Validate TLS Key File */
	if conf.TLSKeyPath == "" {
		return fmt.Errorf("TLS Key File is required. eldim works only with HTTPS")
	}
	f, err = os.Open(conf.TLSKeyPath)
	if err != nil {
		return fmt.Errorf("Failed to open TLS Key File: %v", err)
	}
	err = f.Close()
	if err != nil {
		return fmt.Errorf("Failed to close TLS Key File: %v", err)
	}

	/* Validate Backends */
	for _, b := range conf.SwiftBackends {
		err = b.Validate()
		if err != nil {
			return fmt.Errorf("Failed to validate OpenStack Swift Backend '%s': %v", b.Name(), err)
		}
	}
	for _, b := range conf.GCSBackends {
		err := b.Validate()
		if err != nil {
			return fmt.Errorf("Failed to validate Google Cloud Storage Backend '%s': %v", b.Name(), err)
		}
	}
	for _, b := range conf.S3Backends {
		err := b.Validate()
		if err != nil {
			return fmt.Errorf("Failed to validate S3 Backend '%s': %v", b.Name(), err)
		}
	}

	/* Ensure there is at least one backend */
	if len(conf.SwiftBackends)+len(conf.GCSBackends)+len(conf.S3Backends) == 0 {
		return fmt.Errorf("eldim needs at least one backend to operate, 0 found")
	}

	/* Validate Max Upload RAM (in MB) */
	if conf.MaxUploadRAM <= 0 {
		return fmt.Errorf("Maximum Upload RAM must be a positive number")
	}

	/* Validate Encryption Key */
	if conf.EncryptionKey != "" {
		return fmt.Errorf("Use of encryption key is deprecated since v0.6.0. Please consult the docs")
	}
	if len(conf.Encryption.AgeID)+len(conf.Encryption.AgeSSH) == 0 {
		return fmt.Errorf("Please configure at least one age encryption key")
	}
	for _, r := range conf.Encryption.AgeID {
		_, err := age.ParseX25519Recipient(r)
		if err != nil {
			return fmt.Errorf("Failed to parse age Identity '%s': %v", r, err)
		}
	}
	for _, r := range conf.Encryption.AgeSSH {
		_, err := agessh.ParseRecipient(r)
		if err != nil {
			return fmt.Errorf("Failed to parse age ssh key Identity '%s': %v", r, err)
		}
	}

	/* Validate Prometheus Settings */
	if conf.PrometheusEnabled == true {
		/* Only check Prometheus Configuration if Prometheus is enabled */
		if conf.PrometheusAuthUser == "" {
			return fmt.Errorf("You need to set the prometheusauthuser in the configuration file. eldim only works with HTTP Basic Auth for Prometheus Metrics")
		}
		if !regexp.MustCompile("^[a-zA-Z0-9]{20,128}$").MatchString(conf.PrometheusAuthUser) {
			return fmt.Errorf("The prometheusauthuser must contain a-z, A-Z, and 0-9, and must be 20-128 characters long")
		}
		if conf.PrometheusAuthPass == "" {
			return fmt.Errorf("You need to set the prometheusauthpass in the configuration file. eldim only works with HTTP Basic Auth for Prometheus Metrics")
		}
		if !regexp.MustCompile("^[a-zA-Z0-9]{20,128}$").MatchString(conf.PrometheusAuthPass) {
			return fmt.Errorf("The prometheusauthpass must contain a-z, A-Z, and 0-9, and must be 20-128 characters long")
		}
	}

	/*************************
	* Validate Client Config *
	*************************/

	if conf.ClientFile == "" {
		return fmt.Errorf("Did not supply a clients config file")
	}

	/* Attempt to read the Clients File */
	fc, err := ioutil.ReadFile(conf.ClientFile)
	if err != nil {
		return fmt.Errorf("Failed to open clients file: %v", err)
	}

	/* Unmarshal the YAML Clients File */
	var clients []ClientConfig

	err = yaml.Unmarshal(fc, &clients)
	if err != nil {
		return fmt.Errorf("Unable to decode client file YAML: %v", err)
	}

	/* Check if clients have been supplied */
	if len(clients) == 0 {
		return fmt.Errorf("No clients have been supplied. eldim will not work")
	}

	/* Validate clients individually */
	for i, c := range clients {
		err = c.Validate()
		if err != nil {
			return fmt.Errorf("Client '%s' (%d) is invalid: %v", c.Name(), i+1, err)
		}
	}

	/* Check for duplicate names / passwords / IPs */
	var nameSet = make(map[string]bool)
	var passSet = make(map[string]bool)
	var ipSet = make(map[string]bool)
	for i, c := range clients {
		/* Duplicate Name Check */
		if nameSet[c.Name()] {
			return fmt.Errorf("Client %d does not have a unique name: %s", i+1, c.Name())
		}
		nameSet[c.Name()] = true

		/* Duplicate Password Check */
		if c.Password != "" {
			if passSet[c.Password] {
				return fmt.Errorf("Client %d does not have a unique password: %s", i+1, c.Name())
			}
			passSet[c.Password] = true
		}

		/* Duplicate IP Check */
		for _, ip := range append(c.IPv6(), c.IPv4()...) {
			if ipSet[ip.String()] {
				return fmt.Errorf("Client '%s' (%d) reuses an IP Address: %s", c.Name(), i+1, ip.String())
			}
			ipSet[ip.String()] = true
		}
	}

	return nil
}

/*
Clients returns all configured Backend Clients
*/
func (conf *Config) Clients() []backend.Client {
	var ret []backend.Client

	/* OpenStack Swift */
	for _, be := range conf.SwiftBackends {
		ret = append(ret,
			swift.New(context.Background(),
				be,
			),
		)
	}

	/* Google Cloud Storage */
	for _, be := range conf.GCSBackends {
		ret = append(ret,
			gcs.New(context.Background(),
				be,
			),
		)
	}

	/* S3 */
	for _, be := range conf.S3Backends {
		ret = append(ret,
			s3.New(context.Background(),
				be,
			),
		)
	}

	/* Return all clients */
	return ret
}

/*
ClientConfig is the data structure containing all information about
a client that can connect to the eldim service
*/
type ClientConfig struct {
	ClientName string   `yaml:"name"`
	IPv4Addr   []string `yaml:"ipv4"`
	IPv6Addr   []string `yaml:"ipv6"`
	Password   string   `yaml:"password"`
}

/*
Validate validates a single client entry from the client configuration
file
*/
func (client *ClientConfig) Validate() error {
	/* Check if client has name */
	if client.ClientName == "" {
		return fmt.Errorf("Client has no name")
	}

	/* Make sure all IP Addresses can be parsed */
	for _, ip := range append(client.IPv6(), client.IPv4()...) {
		if ip == nil {
			return fmt.Errorf("Client contains an invalid IP Address")
		}
	}

	/* Ensure IPv4 are in IPv4 and IPv6 are in IPv6 */
	for _, v4 := range client.IPv4() {
		if !strings.Contains(v4.String(), ".") {
			return fmt.Errorf("Client contains a non-IPv4 in IPv4 list: %s", v4.String())
		}
	}
	for _, v6 := range client.IPv6() {
		if !strings.Contains(v6.String(), ":") || strings.Contains(v6.String(), ".") {
			return fmt.Errorf("Client contains a non-IPv6 in IPv6 list: %s", v6.String())
		}
	}

	/* Ensure there is at least one of (password, IP) */
	if client.Password == "" && len(client.IPv4()) == 0 && len(client.IPv6()) == 0 {
		return fmt.Errorf("Client does not have at least one of (password, IPv6, IPv4)")
	}

	/* Enforce client authentication password policy */
	if len(client.Password) < 32 && client.Password != "" {
		return fmt.Errorf("Client has a password shorter than 32 characters: 32-128 are acceptable")
	}
	if len(client.Password) > 128 {
		return fmt.Errorf("Client has a password longer than 128 characters: 32-128 are acceptable")
	}

	return nil
}

/*
Name returns the name of the client, as configured
*/
func (client *ClientConfig) Name() string {
	if client.ClientName == "" {
		client.ClientName = "Unnamed Client"
	}
	return client.ClientName
}

/*
IPv4 returns the list of client IPv4 Addresses
*/
func (client *ClientConfig) IPv4() []net.IP {
	var ret []net.IP
	for _, ip := range client.IPv4Addr {
		ret = append(ret, net.ParseIP(ip))
	}

	return ret
}

/*
IPv6 returns the list of client IPv6 Addresses
*/
func (client *ClientConfig) IPv6() []net.IP {
	var ret []net.IP
	for _, ip := range client.IPv6Addr {
		ret = append(ret, net.ParseIP(ip))
	}

	return ret
}
