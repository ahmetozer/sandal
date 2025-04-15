package net

import (
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

const charset = "abcdefghijklmnopqrstuvwxyz"

var seededRand *rand.Rand = rand.New(
	rand.NewSource(time.Now().UnixNano()))

func stringWithCharset(length int, charset string) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

func randomString(length int) string {
	return stringWithCharset(length, charset)
}

func parseCmd(cmd string, conts *[]*config.Config) (Link, error) {

	myar := strings.Split(cmd, ";")
	myIf := Link{Id: randomString(10)}

	for v := range myar {
		kv := strings.Split(myar[v], "=")
		if len(kv) <= 1 || kv[1] == "" {
			continue
		}
		switch key := kv[0]; key {
		case "ip":
			for _, ip := range kv[1:] {
				IP, net, err := net.ParseCIDR(ip)
				if err != nil {
					slog.Debug(err.Error())
					continue
				}
				myIf.Addr = append(myIf.Addr, Addr{IP, net})
			}
		case "route":
			for _, ip := range kv[1:] {
				IP, net, err := net.ParseCIDR(ip)
				if err != nil {
					slog.Warn("unable to parse route %s %s", ip, err)
					continue
				}
				myIf.Route = append(myIf.Route, Addr{IP, net})
			}
			if myIf.Route == nil {
				return myIf, fmt.Errorf("unable to parse gateway ip %s", key)
			}
		case "type":
			myIf.Type = kv[1]
		case "master":
			myIf.Master = kv[1]
		case "name":
			myIf.Name = kv[1]
		default:
			return myIf, fmt.Errorf("unexpected property %s", key)
		}
	}
	link := myIf.defaults(conts)
	links, err := ToLinks(&((*conts)[len(*conts)-1].Net))
	if err != nil {
		return link, err
	}
	links.Append(link)

	(*conts)[len(*conts)-1].Net = *links
	slog.Debug("link", "link", link)
	return link, nil

}

func ParseFlag(flags []string, conts []*config.Config, cont *config.Config) (Links, error) {

	slog.Debug("netFlags", "flags", flags)

	links := make(Links, 0)
	if len(flags) == 0 {
		flags = []string{"type=veth"}
	}

	for i := range conts {
		if conts[i].Name == cont.Name {
			conts[i] = conts[len(conts)-1]
			conts = conts[:len(conts)-1]
			break
		}
	}
	// Re place container without addresses for re allocation or re setting ip's
	conts = append(conts, &config.Config{
		Name: cont.Name,
	})

	for i := range flags {
		link, err := parseCmd(flags[i], &conts)
		if err != nil {
			return nil, err
		}

		links = append(links, link)
	}

	return links, nil

}
