package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/JamesClonk/easy-vpn/config"
	"github.com/JamesClonk/easy-vpn/provider"
	"github.com/JamesClonk/easy-vpn/provider/digitalocean"
	"github.com/JamesClonk/easy-vpn/provider/vultr"
	"github.com/JamesClonk/easy-vpn/rng"
	"github.com/JamesClonk/easy-vpn/ssh"
	"github.com/JamesClonk/easy-vpn/vm"
	"github.com/codegangsta/cli"
)

const (
	VERSION            = "1.0.0"
	EASYVPN_IDENTIFIER = "easy-vpn"
)

func main() {
	app := cli.NewApp()
	app.Name = "easy-vpn"
	app.Author = "JamesClonk"
	app.Email = "jamesclonk@jamesclonk.ch"
	app.Version = VERSION
	app.Usage = "a simple tool to spin up a VPN server on a cloud VPS that self-destructs after idle time"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config, c",
			Value: "easy-vpn.toml",
			Usage: "specify which configuration file to use",
		},
		cli.StringFlag{
			Name:  "provider, p",
			Value: "digitalocean",
			Usage: "specify which cloud VPS provider to use",
		},
		cli.StringFlag{
			Name:  "api-key, k",
			Value: "abc123xyz",
			Usage: "API-Key for cloud VPS provider",
		},
		cli.StringFlag{
			Name:  "autoconnect, a",
			Value: "true",
			Usage: "do automatic VPN connect after a VPS was started?",
		},
		cli.StringFlag{
			Name:  "idletime, i",
			Value: "15",
			Usage: "idle time in minutes after which the VPS will self-destruct",
		},
		cli.StringFlag{
			Name:  "uptime, u",
			Value: "360",
			Usage: "maximum uptime in minutes after which the VPS will self-destruct",
		},
	}

	app.Commands = []cli.Command{{
		Name:      "up",
		ShortName: "u",
		Usage:     "spin up a new VPS with a VPN server in it",
		// TODO: add description, explain -r/--region option
		Description: ".....",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "region, r",
				Usage: "specify which region to use for new VPS",
			},
		},
		Action: func(c *cli.Context) {
			startVpn(c)
		},
	}, {
		Name:      "down",
		ShortName: "d",
		Usage:     "shutdown and destroy a VPS",
		// TODO: add description, tell that it requires 1 argument: the VPS name/id to destroy
		Description: ".....",
		Action: func(c *cli.Context) {
			destroyVpn(c)
		},
	}, {
		Name:      "show",
		ShortName: "s",
		Usage:     "shows all current VPN-VPS and their status",
		// TODO: add description, will list all current VPS and their status that match naming criteria
		Description: ".....",
		Action: func(c *cli.Context) {
			showVpn(c)
		},
	}}

	app.Action = func(c *cli.Context) {
		cli.ShowAppHelp(c)
		os.Exit(1)
	}
	app.RunAndExitOnError()
}

func startVpn(c *cli.Context) {
	p := getProvider(c)

	sshkeyId := ssh.GetEasyVpnKeyId(p, EASYVPN_IDENTIFIER)
	machine := vm.GetEasyVpn(p, sshkeyId, EASYVPN_IDENTIFIER)

	fmt.Printf("%q\n", machine) // TODO: prettify

	// check if docker pptpd is already running
	out := ssh.Exec(p, machine.IP, `ps -ef | grep pptpd | grep -v grep; echo "..."`)
	if strings.Contains(out, "pptpd") {
		fmt.Println("pptpd is already running on virtual machine")
	} else {
		// update machine
		log.Println("Update virtual machine")
		ssh.Call(p, machine.IP, `apt-get update -qq`)
		ssh.Call(p, machine.IP, `apt-get install -qy docker.io pptpd iptables`)
		ssh.Exec(p, machine.IP, `service pptpd stop`)

		// generate username & password for pptpd
		username := rng.GenerateUsername()
		password := rng.GeneratePassword()

		// setup docker
		log.Println("Setup docker on virtual machine")
		ssh.Call(p, machine.IP, `service docker.io restart`)
		ssh.Call(p, machine.IP, `docker pull jamesclonk/docker-pptpd`)
		ssh.Exec(p, machine.IP, fmt.Sprintf(`echo "%s * %s *" > /chap-secrets`, username, password))

		// run docker
		log.Println("Run docker-pptpd container on virtual machine")
		ssh.Call(p, machine.IP, `docker run --name pptpd --privileged -d -p 1723:1723 -v /chap-secrets:/etc/ppp/chap-secrets:ro jamesclonk/docker-pptpd`)

		log.Printf("docker-pptpd started, with username[%s] and password[%s]\n", username, password)
	}

}

func destroyVpn(c *cli.Context) {
	p := getProvider(c)
	vm.DestroyEasyVpn(p, EASYVPN_IDENTIFIER)
}

func showVpn(c *cli.Context) {
	p := getProvider(c)
	for _, machine := range vm.GetAll(p) {
		fmt.Printf("%q\n", machine) // TODO: prettify
	}
}

func parseGlobalOptions(c *cli.Context) *config.Config {
	cfg, err := config.LoadConfiguration(c.GlobalString("config"))
	if err != nil {
		log.Fatal(err)
	}

	if c.GlobalIsSet("provider") {
		cfg.Provider = c.GlobalString("provider")
	}

	if c.GlobalIsSet("api-key") {
		cfg.Providers[cfg.Provider] = config.Provider{
			ApiKey: c.GlobalString("api-key"),
			Region: cfg.Providers[cfg.Provider].Region,
			Size:   cfg.Providers[cfg.Provider].Size,
			OS:     cfg.Providers[cfg.Provider].OS,
		}
	}

	if c.GlobalIsSet("autoconnect") {
		cfg.Options.Autoconnect = strings.ToLower(c.GlobalString("autoconnect")) == "true"
	}

	if c.GlobalIsSet("idletime") {
		idletime, err := strconv.ParseInt(c.GlobalString("idletime"), 10, 32)
		if err != nil {
			log.Fatalf("Invalid value for --idletime option given: %v\n",
				c.GlobalString("idletime"))
		}
		cfg.Options.Idletime = int(idletime)
	}

	if c.GlobalIsSet("uptime") {
		uptime, err := strconv.ParseInt(c.GlobalString("uptime"), 10, 32)
		if err != nil {
			log.Fatalf("Invalid value for --uptime option given: %v\n",
				c.GlobalString("uptime"))
		}
		cfg.Options.Uptime = int(uptime)
	}

	return cfg
}

func getProvider(c *cli.Context) provider.API {
	cfg := parseGlobalOptions(c)

	switch cfg.Provider {
	case "digitalocean":
		return digitalocean.DO{Config: cfg}
	case "vultr":
		return vultr.Vultr{Config: cfg}
	case "aws":
		log.Fatal("Not yet implemented!")
	default:
		log.Fatal("Unknown provider!")
	}
	return nil
}
