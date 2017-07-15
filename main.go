package main

import (
	"bufio"
	"crypto/md5"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-fsnotify/fsnotify"
	"github.com/miekg/dns"

	"github.com/spikeekips/resolvable/resolver"

	dockerapi "github.com/fsouza/go-dockerclient"
)

var Version string

func getopt(name, def string) string {
	if env := os.Getenv(name); env != "" {
		return env
	}
	return def
}

func ipAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsMulticast() {
			if ipv4 := ipnet.IP.To4(); ipv4 != nil {
				return ipv4.String(), nil
			}
		}
	}

	return "", errors.New("no addresses found")
}

func parseContainerEnv(containerEnv []string, prefix string) map[string]string {
	parsed := make(map[string]string)

	for _, env := range containerEnv {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		keyVal := strings.SplitN(env, "=", 2)
		if len(keyVal) > 1 {
			parsed[keyVal[0]] = keyVal[1]
		} else {
			parsed[keyVal[0]] = ""
		}
	}

	return parsed
}

func registerContainers(docker *dockerapi.Client, events chan *dockerapi.APIEvents, dns resolver.Resolver, containerDomain string, hostIP net.IP) error {
	// TODO add an options struct instead of passing all as parameters
	// though passing the events channel from an options struct was triggering
	// data race warnings within AddEventListener, so needs more investigation

	if events == nil {
		events = make(chan *dockerapi.APIEvents)
	}
	if err := docker.AddEventListener(events); err != nil {
		return err
	}

	if !strings.HasPrefix(containerDomain, ".") {
		containerDomain = "." + containerDomain
	}

	getAddress := func(container *dockerapi.Container) (net.IP, error) {
		for {
			if container.NetworkSettings.IPAddress != "" {
				return net.ParseIP(container.NetworkSettings.IPAddress), nil
			}

			if container.HostConfig.NetworkMode == "host" {
				if hostIP == nil {
					return nil, errors.New("IP not available with network mode \"host\"")
				} else {
					return hostIP, nil
				}
			}

			if strings.HasPrefix(container.HostConfig.NetworkMode, "container:") {
				otherId := container.HostConfig.NetworkMode[len("container:"):]
				var err error
				container, err = docker.InspectContainer(otherId)
				if err != nil {
					return nil, err
				}
				continue
			}

			return nil, fmt.Errorf("unknown network mode", container.HostConfig.NetworkMode)
		}
	}

	addContainer := func(containerId string) error {
		container, err := docker.InspectContainer(containerId)
		if err != nil {
			return err
		}
		addr, err := getAddress(container)
		if err != nil {
			return err
		}

		err = dns.AddHost(containerId, addr, container.Config.Hostname, container.Name[1:]+containerDomain)
		if err != nil {
			return err
		}

		env := parseContainerEnv(container.Config.Env, "DNS_")
		if dnsDomains, ok := env["DNS_RESOLVES"]; ok {
			if dnsDomains == "" {
				return errors.New("empty DNS_RESOLVES, should contain a comma-separated list with at least one domain")
			}

			port := 53
			if portString := env["DNS_PORT"]; portString != "" {
				port, err = strconv.Atoi(portString)
				if err != nil {
					return errors.New("invalid DNS_PORT \"" + portString + "\", should contain a number")
				}
			}

			domains := strings.Split(dnsDomains, ",")
			err = dns.AddUpstream(containerId, addr, port, domains...)
			if err != nil {
				return err
			}
		}

		if bridge := container.NetworkSettings.Bridge; bridge != "" {
			bridgeAddr := net.ParseIP(container.NetworkSettings.Gateway)
			err = dns.AddHost("bridge:"+bridge, bridgeAddr, bridge)
			if err != nil {
				return err
			}
		}

		return nil
	}

	containers, err := docker.ListContainers(dockerapi.ListContainersOptions{})
	if err != nil {
		return err
	}

	for _, listing := range containers {
		if err := addContainer(listing.ID); err != nil {
			log.Printf("error adding container %s: %s\n", listing.ID[:12], err)
		}
	}

	for msg := range events {
		go func(msg *dockerapi.APIEvents) {
			switch msg.Status {
			case "start":
				if err := addContainer(msg.ID); err != nil {
					log.Printf("error adding container %s: %s\n", msg.ID[:12], err)
				}
			case "die":
				dns.RemoveHost(msg.ID)
				dns.RemoveUpstream(msg.ID)
			}
		}(msg)
	}

	return errors.New("docker event loop closed")
}

var hostsFile string = "/tmp/hosts"

var hostsFileMap map[string][]string

func readHostsFile(dns resolver.Resolver) {
	log.Printf("trying to read hosts file, `%v`", hostsFile)

	if hostsFileMap == nil {
		hostsFileMap = map[string][]string{}
	}

	nextHostsFileMap := map[string][]string{}

	inFile, _ := os.Open(hostsFile)
	defer inFile.Close()
	scanner := bufio.NewScanner(inFile)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		line := scanner.Text()

		r0, _ := regexp.Compile("#.*")
		r1, _ := regexp.Compile("[\\s][\\s]*")
		seperated := r1.Split(r0.ReplaceAllString(line, ""), -1)

		if len(seperated) < 2 {
			log.Printf("error: invalid host line, skipped: `%s`", line)
			continue
		}

		if net.ParseIP(seperated[0]) == nil {
			log.Printf("error: invalid host line, skipped: `%s`", line)
			continue
		}

		for i := 1; i < len(seperated); i++ {
			nextHostsFileMap[fmt.Sprintf("%x", md5.Sum([]byte(seperated[i])))] =
				[]string{seperated[0], seperated[i]}
		}
	}
	log.Printf("found %v records from hosts file, `%v`.", len(nextHostsFileMap), nextHostsFileMap)

	isSameRecord := func(a []string, b []string) bool {
		return md5.Sum([]byte(strings.Join(a, ":"))) == md5.Sum([]byte(strings.Join(b, ":")))
	}

	{
		for k, v := range nextHostsFileMap {
			if old, ok := hostsFileMap[k]; ok {
				if isSameRecord(old, v) {
					log.Printf("same host and same ip: `%v`.", v)
					continue
				} else {
					log.Printf("same host, but different ip: `%v`.", v)
				}
			}
			dns.RemoveHost(k)
			if dns.AddHost(k, net.ParseIP(v[0]), v[1]) != nil {
				log.Printf("failed to add the host, `%v`.", v)
				continue
			}
			log.Printf("newly added the host, %v: %v", k, v)
		}
	}

	{
		for k, v := range hostsFileMap {
			if _, ok := nextHostsFileMap[k]; ok {
				continue
			}
			if dns.RemoveHost(k) != nil {
				log.Printf("failed to remove the host, `%v`.", v)
				continue
			}
			log.Printf("removed the host, %v: %v", k, v)
		}
	}

	hostsFileMap = nextHostsFileMap
}

func monitorHostsFile(dns resolver.Resolver) error {
	hostsFile = getopt("HOSTS_FILE", hostsFile)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	loadFile := func() {
		log.Printf("trying to load hosts file, `%v`", hostsFile)
		for {
			if _, err := os.Stat(hostsFile); err != nil || os.IsNotExist(err) {
				log.Printf("failed to find `%s`", hostsFile)
				time.Sleep(3000 * time.Millisecond)

				continue
			}
			break
		}
		err = watcher.Add(hostsFile)
		if err != nil {
			log.Fatal(err)
		}
	}

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("modified file:", event.Name)
					readHostsFile(dns)
				} else if event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("removed file:", event.Name)
					loadFile()
					readHostsFile(dns)
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()

	loadFile()
	readHostsFile(dns)

	<-make(chan bool)
	return nil
}

func run() error {
	// set up the signal handler first to ensure cleanup is handled if a signal is
	// caught while initializing
	exitReason := make(chan error)
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		sig := <-c
		log.Println("exit requested by signal:", sig)
		exitReason <- nil
	}()

	address, err := ipAddress()
	if err != nil {
		return err
	}
	log.Println("got local address:", address)

	for name, conf := range resolver.HostResolverConfigs.All() {
		err := conf.StoreAddress(address)
		if err != nil {
			log.Printf("[ERROR] error in %s: %s", name, err)
		}
		defer conf.Clean()
	}

	var hostIP net.IP
	if envHostIP := os.Getenv("HOST_IP"); envHostIP != "" {
		hostIP = net.ParseIP(envHostIP)
		log.Println("using address for --net=host:", hostIP)
	}

	dnsResolver, err := resolver.NewResolver()
	if err != nil {
		return err
	}
	defer dnsResolver.Close()

	if err = dnsResolver.Listen(); err != nil {
		log.Panicf("[error] %v", err)
	}
	log.Printf("Listening port, %d...", dnsResolver.Port)

	// Docker
	var docker *dockerapi.Client
	var localDomain string

	without_docker := getopt("WITHOUT_DOCKER", "0") == "1"
	log.Printf("WITHOUT_DOCKER=%v", without_docker)
	if !without_docker {
		docker, err = dockerapi.NewClient(getopt("DOCKER_HOST", "unix:///tmp/docker.sock"))
		if err != nil {
			return err
		}

		var gateway net.IP
		out, _ := exec.Command("ip", "route").Output()
		for _, v := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(v, "default ") {
				continue
			}
			gateway = net.ParseIP(strings.Split(v, " ")[2])
			break
		}
		if gateway == nil {
			return errors.New("failed to find the gateway address.")
		}

		dockerhost_name := getopt("DOCKERHOST_NAME", "dockerhost")
		err = dnsResolver.AddHost(dockerhost_name, gateway, dockerhost_name, dockerhost_name)
		if err != nil {
			return errors.New("failed to add the gateway address.")
		}
		log.Printf("registered gateway ip, `%v` as `%s`", gateway, dockerhost_name)

		localDomain = "docker"
		dnsResolver.AddUpstream(localDomain, nil, 0, localDomain)
	}

	resolvConfig, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return err
	}
	resolvConfigPort, err := strconv.Atoi(resolvConfig.Port)
	if err != nil {
		return err
	}
	for _, server := range resolvConfig.Servers {
		if server != address {
			dnsResolver.AddUpstream("resolv.conf:"+server, net.ParseIP(server), resolvConfigPort)
		}
	}

	go func() {
		dnsResolver.Wait()
		exitReason <- errors.New("dns resolver exited")
	}()

	if !without_docker {
		go func() {
			exitReason <- registerContainers(docker, nil, dnsResolver, localDomain, hostIP)
		}()
	}

	go func() {
		exitReason <- monitorHostsFile(dnsResolver)
	}()

	return <-exitReason
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(Version)
		os.Exit(0)
	}
	log.Printf("Starting resolvable %s ...", Version)

	err := run()
	if err != nil {
		log.Fatal("resolvable: ", err)
	}
}
