// on startup
//
// for each configured user:
// - check existance of <envs>/<user>/env
//    => no
//    - run <setup script>
//      (expects creation of <env_dir>/<user>/env)
// - for each user
//    - start jupyter-notebook instance
//    - write down a) port and b) token
//    - register session
//
// request /entry/<token>/<user>
// 1. check that token is eq. to <auth. token>
// 2. check that <user> is in <allowed-users> list
// 3. retrieve session using <user>
// 4. redirect to /proxy/<token>/<user>/?token=<jupyter token>
//
// request /proxy/<token>/<user>/*
//
// 1. check that token is eq. to <auth. token>
// 2. check that <user> is in <allowed-users> list
// 3. retrieve session using <user>
// 4. proxy localhost:<jupyter port>
//
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var flag_config *string = flag.String("config", "jupyter-launch.json", "Path to the config file")

type Config struct {
	Users       []string
	Token       string
	EnvDir      string `json:"env_dir"`
	SetupScript string `json:"setup_script"`
	StartScript string `json:"start_script"`
	StopScript string `json:"stop_script"`
	BasePort    int    `json:"base_port"`
}

var config Config
var sessions map[string]*SessionHandler

func readConfig(path string) (c Config) {
	cfg_bytes, err := ioutil.ReadFile(path)

	if err != nil {
		log.Fatalf("Error reading config %s: %s", *flag_config, err)
	}

	if err := json.Unmarshal(cfg_bytes, &c); err != nil {
		log.Fatal("Reading config failed:", err)
	}

	return
}

func checkScript(path string) error {
	if len(path) == 0 {
		return errors.New("Empty path to script.")
	}

	stat, err := os.Stat(path)

	if err != nil {
		return err
	}

	if stat.IsDir() {
		return errors.New("Not a file")
	}

	return nil
}

func validateToken(tok string) bool {
	if len(tok) != len(config.Token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(config.Token)) == 1
}

func validateUser(user string) bool {
	for _, needle := range config.Users {
		if needle == user {
			return true
		}
	}
	return false
}

func assertPermission(w http.ResponseWriter, vars map[string]string) bool {
	valid := validateToken(vars["token"]) && validateUser(vars["user"])
	if !valid {
		http.Error(w, "Not authorized", 401)
	}
	return valid
}

type SessionHandler struct {
	User       string
	Port       int
	Token      string
	CancelFunc context.CancelFunc
}

func isUpgrade(r *http.Request) bool {
	if v, ok := r.Header["Connection"]; ok {
		for _, e := range v {
			if strings.Contains(e, "Upgrade") {
				return true
			}
		}
	}
	return false
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func wsCopy(rc, wc *websocket.Conn) error {
	defer rc.Close()
	defer wc.Close()
	for {
		messageType, r, err := rc.NextReader()
		if err != nil {
			return err
		}
		w, err := wc.NextWriter(messageType)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
}

func (s *SessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := fmt.Sprintf("localhost:%d", s.Port)
	r.Host = host
	r.RequestURI = ""
	r.URL.Scheme = "http"
	r.URL.Host = host

	log.Println("requesting to", r.URL)

	r.Header["Origin"] = []string{"http://" + host}

	if isUpgrade(r) {
		r.URL.Scheme = "ws"
		r.Host = host
		log.Println("dialing to", r.URL.String())

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}

		challenge := r.Header.Get("Sec-Websocket-Key")

		c, r2, err := websocket.DefaultDialer.DialContextExisting(
			context.Background(),
			r.URL.String(),
			r,
			challenge,
		)

		if err != nil {
			log.Println("SAD!", challenge, err, r2.Header)
			return
		}

		go wsCopy(conn, c)
		go wsCopy(c, conn)

		return
	}

	client := &http.Client{}
	resp, err := client.Do(r)

	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h := w.Header()
	for k, v := range resp.Header {
		h[k] = v
	}

	io.Copy(w, resp.Body)
	w.WriteHeader(resp.StatusCode)
}

func getJupyterHandler(user string) (*SessionHandler, error) {
	handler, ok := sessions[user]

	if ok {
		return handler, nil
	}

	return nil, errors.New("no such session setup at startup")
}

func entryHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	if !assertPermission(w, vars) {
		return
	}

	handler, err := getJupyterHandler(vars["user"])

	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	url := fmt.Sprintf(
		"/proxy/%s/%s/?token=%s",
		config.Token,
		vars["user"],
		handler.Token,
	)

	http.Redirect(w, r, url, 302)
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	if !assertPermission(w, vars) {
		return
	}

	handler, err := getJupyterHandler(vars["user"])

	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	handler.ServeHTTP(w, r)
}

func generateToken() (string, error) {
	p := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, p); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(p), nil
}

func scriptCommand(path string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, path, args...)
	return cmd, cancelFunc
}

func computeBaseURL(user string) string {
	return fmt.Sprintf("/proxy/%s/%s/", config.Token, user)
}

func setupJupyter(user string) error {
	cmd, _ := scriptCommand(config.SetupScript, user, config.EnvDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Println(string(out))
	} else {
		log.Println(string(out))
	}
	return err
}

func stopJupyter(user string, port int) error {
	cmd, _ := scriptCommand(
		config.StopScript,
		user,
		config.EnvDir,
		strconv.Itoa(port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Println(string(out))
	}
	return err
}

func startJupyter(
	user string,
	port int,
	token string,
	cancelChan chan<- context.CancelFunc,
) {
	baseURL := computeBaseURL(user)
	cmd, _ := scriptCommand(
		config.StartScript,
		user,
		config.EnvDir,
		baseURL,
		token,
		strconv.Itoa(port))

	cancelFunc := context.CancelFunc(func() {
		cmd.Process.Kill()
	})

	cancelChan <- cancelFunc

	out, err := cmd.CombinedOutput()

	if err != nil {
		log.Println(string(out))
	}
}

func startJupyterAsync(user string, port int, token string) (context.CancelFunc, error) {
	ch := make(chan context.CancelFunc)

	log.Printf("Starting jupyter for %s on %d.\n", user, port)
	go startJupyter(user, port, token, ch)

	cancelFunc := <-ch

	return cancelFunc, nil
}

func setupAndStartJupyter(user string) error {
	token, err := generateToken()

	if err != nil {
		return err
	}

	port := config.BasePort + len(sessions)

	if err := setupJupyter(user); err != nil {
		return err
	}

	cancelFunc, err := startJupyterAsync(user, port, token)
	if err != nil {
		return err
	}

	sessions[user] = &SessionHandler{
		User:       user,
		Port:       port,
		Token:      token,
		CancelFunc: cancelFunc,
	}

	return nil
}

func killSessions() {
	for user, session := range sessions {
		log.Println("Shutting down session for", user)
		session.CancelFunc()
		stopJupyter(user, session.Port)
	}
}

func main() {
	flag.Parse()

	gracefulStop := make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGHUP)
	signal.Notify(gracefulStop, syscall.SIGTERM)

	go func() {
		<-gracefulStop
		killSessions()
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}()

	sessions = make(map[string]*SessionHandler)
	config = readConfig(*flag_config)

	if len(config.Token) == 0 {
		log.Fatal("You must set a token for security.")
	}

	if err := checkScript(config.SetupScript); err != nil {
		log.Fatal("SetupScript check failed: ", err)
	}

	if err := checkScript(config.StartScript); err != nil {
		log.Fatal("StartScript check failed: ", err)
	}

	for _, user := range config.Users {
		log.Println("Setup and start for", user)
		if err := setupAndStartJupyter(user); err != nil {
			log.Fatal("Setup for user ", user, " failed:", err)
		}
	}

	log.Printf("%#v", config)

	router := mux.NewRouter()
	router.StrictSlash(true)
	router.HandleFunc("/entry/{token}/{user}", entryHandler)
	router.HandleFunc("/proxy/{token}/{user}/", proxyHandler)
	router.PathPrefix("/proxy/{token}/{user}/").HandlerFunc(proxyHandler)

	http.Handle("/", router)

	log.Fatal(http.ListenAndServe(":8080", nil))
}
