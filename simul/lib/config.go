package lib

import (
	"errors"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/ConsenSys/handel"
	"github.com/ConsenSys/handel/bn256"
	"github.com/ConsenSys/handel/network"
	"github.com/ConsenSys/handel/network/quic"
	"github.com/ConsenSys/handel/network/udp"
)

// Message that will get signed
var Message = []byte("Everything that is beautiful and noble is the product of reason and calculation.")

// Config is read from a TOML encoded file and passed to Platform.Config and
// prepares the platform for specific system-wide configurations.
type Config struct {
	// which network should we use
	// Valid value: "udp" (default)
	Network string
	// which "curve system" should we use
	// Valid value: "bn256" (default)
	Curve string
	// which encoding should we use on the network
	// valid value: "gob" (default)
	Encoding string
	// which is the port to send measurements to
	MonitorPort int
	// Debug forwards the debug output if set to != 0
	Debug int
	// Maximum time to wait for the whole thing to finish
	// string because of ugly format of TOML encoding ---
	MaxTimeout string
	// how many time should we repeat each experiment
	Retrials int
	// to which file should we write the results
	ResultFile string
	// config for each run
	Runs []RunConfig
}

// MaxNodes returns the maximum number of nodes to test
func (c *Config) MaxNodes() int {
	max := 0
	for _, rc := range c.Runs {
		if max < rc.Nodes {
			max = rc.Nodes
		}
	}
	return max
}

// RunConfig is the config holding parameters for a specific run. A platform can
// start multiple runs sequentially with different parameters each.
type RunConfig struct {
	// How many nodes should we spin for this run
	Nodes int
	// threshold of signatures to wait for
	Threshold int
	// extra for particular information for specific platform for examples
	Extra interface{}
	// XXX NOT USED YET
	//Failing   int
}

// LoadConfig looks up the given file to unmarshal a TOML encoded Config.
func LoadConfig(path string) *Config {
	c := new(Config)
	_, err := toml.DecodeFile(path, c)
	if err != nil {
		panic(err)
	}
	return c
}

// WriteTo writes the config to the specified file path.
func (c *Config) WriteTo(path string) error {
	file, err := os.Create(path)
	defer file.Close()
	if err != nil {
		return err
	}

	enc := toml.NewEncoder(file)
	return enc.Encode(c)
}

// NewNetwork returns the network implementation designated by this config for this
// given identity
func (c *Config) NewNetwork(id handel.Identity) handel.Network {
	if c.Network == "" {
		c.Network = "udp"
	}
	net, err := c.selectNetwork(id)
	if err != nil {
		panic(err)
	}
	return net
}

func (c *Config) selectNetwork(id handel.Identity) (handel.Network, error) {
	encoding := c.NewEncoding()
	switch c.Network {
	case "udp":
		return udp.NewNetwork(id.Address(), encoding)
	case "quic":
		return quic.NewNetwork(id.Address(), encoding)
	default:
		return nil, errors.New("not implemented yet")
	}
}

// NewEncoding returns the corresponding network encoding
func (c *Config) NewEncoding() network.Encoding {
	if c.Encoding == "" {
		c.Encoding = "gob"
	}
	switch c.Encoding {
	case "gob":
		return network.NewGOBEncoding()
	default:
		panic("not implemented yet")
	}
}

// NewConstructor returns a Constructor that is using the curve denoted by the
// curve field of the config. Valid input so far is "bn256".
func (c *Config) NewConstructor() Constructor {
	if c.Curve == "" {
		c.Curve = "bn256"
	}
	switch c.Curve {
	case "bn256":
		return &handelConstructor{bn256.NewConstructor()}
	default:
		panic("not implemented yet")
	}
}

// GetMaxTimeout returns the global maximum timeout specified in the config
func (c *Config) GetMaxTimeout() time.Duration {
	dd, err := time.ParseDuration(c.MaxTimeout)
	if err != nil {
		panic(err)
	}
	return dd
}

// Duration is an alias for time.Duration
type Duration time.Duration

// UnmarshalText implements the TextUnmarshaler interface
func (d *Duration) UnmarshalText(text []byte) error {
	dd, err := time.ParseDuration(string(text))
	if err == nil {
		*d = Duration(dd)
	}
	return err
}

// MarshalText implements the TextMarshaler interface
func (d *Duration) MarshalText() ([]byte, error) {
	str := time.Duration(*d).String()
	return []byte(str), nil
}
