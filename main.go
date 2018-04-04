package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hashicorp/raft"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//type proxy{}

var (
	config     *Config
	serverNode *node
)

type Config struct {
	RaftAddress net.Addr
	HTTPAddress net.Addr
	JoinAddress string
	DataDir     string
	Bootstrap   bool
}

func main() {

	pwd, err := os.Getwd()
	if err != nil {
		pwd = "."
	}

	defaultDataPath := filepath.Join(pwd, "raft")

	dataDir := flag.String("datafolder", defaultDataPath, "Path in which to store Raft data")
	BindAddress := flag.String("addr", getIP(), "IP Address on which to bind")
	RaftPort := flag.Int("rport", 7000, "Port on which to bind Raft")
	HTTPPort := flag.Int("hport", 8000, "Port on which to bind HTTP")
	JoinAddress := flag.String("join", "", "http address of another node to join. Any active node in the cluster")
	Bootstrap := flag.Bool("bootstrap", false, "Bootstrap the cluster with this node, required on 1st node only")

	flag.Parse()

	config = &Config{
		DataDir: *dataDir,
		HTTPAddress: &net.TCPAddr{
			IP:   net.ParseIP(*BindAddress),
			Port: *HTTPPort,
		},
		RaftAddress: &net.TCPAddr{
			IP:   net.ParseIP(*BindAddress),
			Port: *RaftPort,
		},
		JoinAddress: *JoinAddress,
		Bootstrap:   *Bootstrap,
	}

	serverNode, err = NewNode(config)
	go leaderUpdater(serverNode.raft.LeaderCh())

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error configuring node: %s", err)
		os.Exit(1)
	}

	if config.JoinAddress != "" {
		go func() {
			retryJoin := func() error {
				joinUrl := url.URL{
					Scheme: "http",
					Host:   config.JoinAddress,
					Path:   "join",
				}

				req, err := http.NewRequest(http.MethodPost, joinUrl.String(), nil)
				if err != nil {
					return err
				}
				req.Header.Add("Peer-Address", config.RaftAddress.String())

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return err
				}

				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("non 200 status code: %d", resp.StatusCode)
				}

				return nil
			}

			for {
				if err := retryJoin(); err != nil {
					fmt.Fprintf(os.Stderr, "Error configuring node: %s", err)
					time.Sleep(1 * time.Second)
				} else {
					break
				}
			}
		}()
	}

	s := &http.Server{
		Addr: config.HTTPAddress.String(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMaster() {
				if strings.Contains(r.URL.Path, "/join") {
					joinHttpHandler(w, r)
				} else {
					masterHttpHandler(w, r)
				}
			} else {
				fmt.Println("got request, but i'm slave node, passing to master")
				slaveHttpHandler(w, r)
			}
		}),

		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	log.Fatal(s.ListenAndServe())
}

func getWebLeaderIP() string {
	return serverNode.fsm.webLeaderAddr
}

func isMaster() bool {
	leaderIP := fmt.Sprint(serverNode.raft.Leader())
	if leaderIP == config.RaftAddress.String() {
		return true
	}
	return false
}

func getIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		os.Stderr.WriteString("failed to get local interfaces: " + err.Error() + "\n")
		os.Exit(1)
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func masterHttpHandler(w http.ResponseWriter, req *http.Request) {
	resp := fmt.Sprintf("responce from master node at %s", config.HTTPAddress)
	fmt.Fprintf(w, resp)
}

func slaveHttpHandler(w http.ResponseWriter, req *http.Request) {

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	masterUrl := fmt.Sprintf("http://%s%s", getWebLeaderIP(), req.RequestURI)
	proxyReq, err := http.NewRequest(req.Method, masterUrl, bytes.NewReader(body))
	proxyReq.Header = req.Header
	proxyReq.Header.Set("Host", req.Host)
	proxyReq.Header.Set("X-Forwarded-For", req.RemoteAddr)
	httpClient := http.Client{}
	resp, err := httpClient.Do(proxyReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	io.Copy(w, resp.Body)
}

func joinHttpHandler(w http.ResponseWriter, req *http.Request) {
	peerAddress := req.Header.Get("Peer-Address")
	if peerAddress == "" {
		log.Println("Peer-Address not set on request")
		w.WriteHeader(http.StatusBadRequest)
	}

	addPeerFuture := serverNode.raft.AddVoter(
		raft.ServerID(peerAddress), raft.ServerAddress(peerAddress), 0, 0)
	if err := addPeerFuture.Error(); err != nil {
		log.Printf("%s\n peer.remoteaddr %s. Error joining peer to Raft", err, peerAddress)

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("peer.remoteaddr %s,  Peer joined Raft", peerAddress)
	w.WriteHeader(http.StatusOK)
}

func leaderUpdater(ch <-chan bool) {
	done := make(chan bool, 1)
	for {
		v := <-ch
		if v == true {
			//to_do add leader web addr to storage
			fmt.Println("###########i WON!!!###############")
			leaderAddr := config.HTTPAddress.String()
			pushNewLeader(leaderAddr)
			go kickUnresponsivePeers(done)
		} else {
			done <- true
		}
	}
}

func pushNewLeader(addr string) {

	event := &event{
		Type:  "set",
		Value: addr,
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		fmt.Println(err)
	}

	applyFuture := serverNode.raft.Apply(eventBytes, 5*time.Second)
	if err := applyFuture.Error(); err != nil {
		fmt.Printf("failed to update %s as leader in raft cluster log", addr)
		return
	}
}

func kickUnresponsivePeers(done chan bool) {
	for {
		select {
		case <-done:
			return
		default:
			conf := serverNode.raft.GetConfiguration()
			err := conf.Error()
			if err != nil {
				fmt.Println("can't get config after leader setup")
			}
			for _, server := range conf.Configuration().Servers {

				serverID := server.ID

				for i := 1; i <= 3; i++ {
					conn, _ := net.DialTimeout("tcp", fmt.Sprintf("%s", serverID), time.Second*5)
					if conn != nil {
						conn.Close()
						break
					}
					if i == 3 {
						fmt.Printf("Raft Node: %s is not responcive, removing it from peers list.", serverID)
						serverNode.raft.RemoveServer(serverID, 0, 0)
					}

				}

			}
			time.Sleep(time.Second * 30)
		}
	}

}
