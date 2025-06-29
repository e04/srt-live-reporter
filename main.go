package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	srt "github.com/datarhei/gosrt"
	"github.com/gorilla/websocket"
)

// listenerConn wraps a srt.Conn and its parent srt.Listener
// to ensure both are closed properly. It satisfies the io.ReadWriteCloser interface.
type listenerConn struct {
	srt.Conn
	listener srt.Listener
}

// Close closes both the connection and the underlying listener.
// This resolves the method ambiguity and correctly implements io.Closer.
func (lc listenerConn) Close() error {
	// The listener's Close() doesn't return an error.
	lc.listener.Close()
	// The connection's Close() returns an error, which we will propagate.
	return lc.Conn.Close()
}

type writer interface {
	io.WriteCloser
}

type nonblockingWriter struct {
	dst  io.WriteCloser
	buf  *bytes.Buffer
	lock sync.RWMutex
	size int
	done bool
}

func newNonblockingWriter(writer io.WriteCloser, size int) writer {
	u := &nonblockingWriter{
		dst:  writer,
		buf:  new(bytes.Buffer),
		size: size,
		done: false,
	}

	if u.size <= 0 {
		u.size = 2048
	}

	go u.writer()

	return u
}

func (u *nonblockingWriter) Write(p []byte) (int, error) {
	if u.done {
		return 0, io.EOF
	}

	u.lock.Lock()
	defer u.lock.Unlock()

	return u.buf.Write(p)
}

func (u *nonblockingWriter) Close() error {
	u.done = true

	u.dst.Close()

	return nil
}

func (u *nonblockingWriter) writer() {
	p := make([]byte, u.size)

	for {
		u.lock.RLock()
		n, err := u.buf.Read(p)
		u.lock.RUnlock()

		if n == 0 || err == io.EOF {
			if u.done {
				break
			}

			time.Sleep(10 * time.Millisecond)
			continue
		}

		if _, err := u.dst.Write(p[:n]); err != nil {
			break
		}
	}

	u.done = true
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mutex      sync.RWMutex
}

func newHub() *hub {
	return &hub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

func (h *hub) run() {
	for {
		select {
		case client := <-h.register:
			h.mutex.Lock()
			h.clients[client] = true
			h.mutex.Unlock()
			log.Printf("WebSocket client connected. Total clients: %d", len(h.clients))

		case client := <-h.unregister:
			h.mutex.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
			h.mutex.Unlock()
			log.Printf("WebSocket client disconnected. Total clients: %d", len(h.clients))

		case message := <-h.broadcast:
			h.mutex.RLock()
			for client := range h.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					delete(h.clients, client)
					client.Close()
				}
			}
			h.mutex.RUnlock()
		}
	}
}

type statsMessage struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"` // "writer" or "reader"
	Stats     *srt.Statistics `json:"stats"`
}

type stats struct {
	period time.Duration
	last   time.Time

	reader io.ReadCloser
	writer io.WriteCloser
	hub    *hub
}

func (s *stats) init(period time.Duration, reader io.ReadCloser, writer io.WriteCloser, hub *hub) {
	s.period = period
	s.last = time.Now()
	s.reader = reader
	s.writer = writer
	s.hub = hub

	go s.tick()
}

func (s *stats) tick() {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()

	for c := range ticker.C {
		if srtconn, ok := s.writer.(srt.Conn); ok {
			stats := &srt.Statistics{}
			srtconn.Stats(stats)

			if stats.Instantaneous.MbpsSentRate > 0 {
				fmt.Fprintf(os.Stderr, "MbpsSentRate: %.2f Mbps\n", stats.Instantaneous.MbpsSentRate)

				if s.hub != nil {
					writerMsg := statsMessage{
						Timestamp: c,
						Type:      "writer",
						Stats:     stats,
					}
					if jsonData, err := json.Marshal(writerMsg); err == nil {
						select {
						case s.hub.broadcast <- jsonData:
						default:
						}
					}
				}
			}
		}

		if srtconn, ok := s.reader.(srt.Conn); ok {
			stats := &srt.Statistics{}
			srtconn.Stats(stats)

			if stats.Instantaneous.MbpsRecvRate > 0 {
				fmt.Fprintf(os.Stderr, "MbpsRecvRate: %.2f Mbps\n", stats.Instantaneous.MbpsRecvRate)

				if s.hub != nil {
					readerMsg := statsMessage{
						Timestamp: c,
						Type:      "reader",
						Stats:     stats,
					}
					if jsonData, err := json.Marshal(readerMsg); err == nil {
						select {
						case s.hub.broadcast <- jsonData:
						default:
						}
					}
				}
			}
		}
	}
}

func handleWebSocket(hub *hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	hub.register <- conn

	go func() {
		defer func() {
			hub.unregister <- conn
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

func isSRT(addr string) bool {
	return strings.HasPrefix(addr, "srt://")
}

func main() {
	var from string
	var to string
	var wsPort int

	flag.StringVar(&from, "from", "", "Address to read from, sources: srt://, udp://, - (stdin)")
	flag.StringVar(&to, "to", "", "Address to write to, targets: srt://, udp://, file://, - (stdout)")
	flag.IntVar(&wsPort, "wsport", 8888, "WebSocket server port (0 to disable)")

	flag.Parse()

	var hub *hub
	if wsPort > 0 {
		hub = newHub()
		go hub.run()

		http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			handleWebSocket(hub, w, r)
		})

		go func() {
			log.Printf("WebSocket server starting on port %d", wsPort)
			if err := http.ListenAndServe(fmt.Sprintf(":%d", wsPort), nil); err != nil {
				log.Printf("WebSocket server error: %v", err)
			}
		}()
	}

	r, err := openReader(from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: from: %v\n", err)
		flag.PrintDefaults()
		os.Exit(1)
	}

	w, err := openWriter(to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: to: %v\n", err)
		flag.PrintDefaults()
		os.Exit(1)
	}

	doneChan := make(chan error)

	go func() {
		buffer := make([]byte, 2048)

		s := stats{}
		s.init(1*time.Second, r, w, hub)

		for {
			n, err := r.Read(buffer)
			if err != nil {
				if isSRT(from) {
					log.Printf("\nSRT reader error: %v. Attempting to reconnect...", err)
					r.Close()
					for {
						var reconnErr error
						r, reconnErr = openReader(from)
						if reconnErr == nil {
							log.Println("SRT reader reconnected successfully.")
							s.reader = r
							break
						}
						log.Printf("Failed to reconnect reader: %v. Retrying in 5 seconds...", reconnErr)
						time.Sleep(5 * time.Second)
					}
					continue
				}

				doneChan <- fmt.Errorf("read: %w", err)
				return
			}

			if _, err := w.Write(buffer[:n]); err != nil {
				if isSRT(to) {
					log.Printf("\nSRT writer error: %v. Attempting to reconnect...", err)
					w.Close()
					for {
						var reconnErr error
						w, reconnErr = openWriter(to)
						if reconnErr == nil {
							log.Println("SRT writer reconnected successfully.")
							s.writer = w
							break
						}
						log.Printf("Failed to reconnect writer: %v. Retrying in 5 seconds...", reconnErr)
						time.Sleep(5 * time.Second)
					}
					continue
				}

				doneChan <- fmt.Errorf("write: %w", err)
				return
			}
		}
	}()

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt)
		<-quit

		doneChan <- nil
	}()

	if err := <-doneChan; err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
	} else {
		fmt.Fprint(os.Stderr, "\n")
	}

	w.Close()
	r.Close()
}

func openReader(addr string) (io.ReadCloser, error) {
	if len(addr) == 0 {
		return nil, fmt.Errorf("the address must not be empty")
	}

	if addr == "-" {
		if os.Stdin == nil {
			return nil, fmt.Errorf("stdin is not defined")
		}

		return os.Stdin, nil
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "srt" {
		config := srt.DefaultConfig()
		if err := config.UnmarshalQuery(u.RawQuery); err != nil {
			return nil, err
		}

		mode := u.Query().Get("mode")

		if mode == "" {
			mode = "listener"
		}

		if mode == "listener" {
			ln, err := srt.Listen("srt", u.Host, config)
			if err != nil {
				return nil, err
			}

			conn, _, err := ln.Accept(func(req srt.ConnRequest) srt.ConnType {
				if len(config.StreamId) > 0 && config.StreamId != req.StreamId() {
					return srt.REJECT
				}

				req.SetPassphrase(config.Passphrase)

				return srt.PUBLISH
			})
			if err != nil {
				ln.Close()
				return nil, err
			}

			if conn == nil {
				ln.Close()
				return nil, fmt.Errorf("incoming connection rejected")
			}

			// Return our new wrapper struct
			return listenerConn{Conn: conn, listener: ln}, nil
		} else if mode == "caller" {
			conn, err := srt.Dial("srt", u.Host, config)
			if err != nil {
				return nil, err
			}

			return conn, nil
		} else {
			return nil, fmt.Errorf("unsupported mode")
		}
	} else if u.Scheme == "udp" {
		laddr, err := net.ResolveUDPAddr("udp", u.Host)
		if err != nil {
			return nil, err
		}

		conn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return nil, err
		}

		return conn, nil
	}

	return nil, fmt.Errorf("unsupported reader")
}

func openWriter(addr string) (io.WriteCloser, error) {
	if len(addr) == 0 {
		return nil, fmt.Errorf("the address must not be empty")
	}

	if addr == "-" {
		if os.Stdout == nil {
			return nil, fmt.Errorf("stdout is not defined")
		}

		return newNonblockingWriter(os.Stdout, 2048), nil
	}

	if strings.HasPrefix(addr, "file://") {
		path := strings.TrimPrefix(addr, "file://")
		file, err := os.Create(path)
		if err != nil {
			return nil, err
		}

		return newNonblockingWriter(file, 2048), nil
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "srt" {
		config := srt.DefaultConfig()
		if err := config.UnmarshalQuery(u.RawQuery); err != nil {
			return nil, err
		}

		mode := u.Query().Get("mode")
		if mode == "" {
			mode = "listener"
		}

		if mode == "listener" {
			ln, err := srt.Listen("srt", u.Host, config)
			if err != nil {
				return nil, err
			}

			conn, _, err := ln.Accept(func(req srt.ConnRequest) srt.ConnType {
				if len(config.StreamId) > 0 && config.StreamId != req.StreamId() {
					return srt.REJECT
				}

				req.SetPassphrase(config.Passphrase)

				return srt.SUBSCRIBE
			})
			if err != nil {
				ln.Close()
				return nil, err
			}

			if conn == nil {
				ln.Close()
				return nil, fmt.Errorf("incoming connection rejected")
			}

			// Return our new wrapper struct
			return listenerConn{Conn: conn, listener: ln}, nil
		} else if mode == "caller" {
			conn, err := srt.Dial("srt", u.Host, config)
			if err != nil {
				return nil, err
			}

			return conn, nil
		} else {
			return nil, fmt.Errorf("unsupported mode")
		}
	} else if u.Scheme == "udp" {
		raddr, err := net.ResolveUDPAddr("udp", u.Host)
		if err != nil {
			return nil, err
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			return nil, err
		}

		return conn, nil
	}

	return nil, fmt.Errorf("unsupported writer")
}
