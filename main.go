package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"redish/config"
	"redish/core"
	"redish/core/models"
	"strings"
	"syscall"
	"golang.org/x/sys/unix"
)

func setupFlags() {
	flag.IntVar(&config.Port, "port", 7379, "TCP port to listen on")
	flag.StringVar(&config.Host, "host", "0.0.0.0", "Host interface to bind to")
	flag.IntVar(&config.MaxClients, "max-clients", 2000, "Maximum number of simultaneous clients")
	flag.IntVar(&config.CronFrequency, "cron-freq", 1, "Cleanup frequency in seconds")
	flag.IntVar(&config.ExpireSampleSize, "expire-sample-size", 20, "Number of expiring keys sampled during cleanup")
	flag.IntVar(&config.MaxKeys, "max-keys", 1000, "Maximum number of keys")
	flag.StringVar(&config.EvictionPolicy, "eviction-policy", "lfu", "Volatile eviction policy: lfu or lru")
	flag.IntVar(&config.EvictionSampleSize, "eviction-sample-size", 5, "Number of keys sampled for approximate eviction")
	flag.IntVar(&config.LFULogFactor, "lfu-log-factor", 10, "LFU logarithmic increment factor")
	flag.IntVar(&config.LFUDecayMinutes, "lfu-decay-minutes", 1, "Minutes between LFU counter decay steps")
	flag.IntVar(&config.LFUInitialCounter, "lfu-initial-counter", 5, "Initial LFU counter value")
	flag.StringVar(&config.AOFPath, "aof", "appendonly.aof", "Append-only file path")
	flag.Int64Var(&config.AOFRewriteMinSize, "aof-rewrite-min-size", 64*1024*1024, "Minimum AOF size before background rewrite")
	flag.IntVar(&config.AOFFsyncIntervalSeconds, "aof-fsync-interval", 1, "AOF fsync interval in seconds")
	flag.Parse()
}

func main() {
	setupFlags()
	if err := core.OpenAOF(config.AOFPath); err != nil {
		log.Fatalf("failed to open AOF: %v", err)
	}
	defer core.CloseAOF()

	if err := runAsyncServer(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func runAsyncServer() error {
	maxClients := config.MaxClients
	if maxClients <= 0 {
		return fmt.Errorf("max-clients must be greater than zero")
	}
	clients := 0

	var events []unix.EpollEvent = make([]unix.EpollEvent, maxClients)

	serverFD, err := syscall.Socket(syscall.AF_INET, syscall.O_NONBLOCK|syscall.SOCK_STREAM, 0)

	if err != nil {
		return err
	}

	defer syscall.Close(serverFD)

	if err = syscall.SetNonblock(serverFD, true); err != nil {
		return err
	}

	ip4 := net.ParseIP(config.Host).To4()
	if ip4 == nil {
		return fmt.Errorf("invalid IPv4 host: %s", config.Host)
	}

	if err = syscall.Bind(serverFD, &syscall.SockaddrInet4{
		Port: config.Port,
		Addr: [4]byte{ip4[0], ip4[1], ip4[2], ip4[3]},
	}); err != nil {
		return err
	}

	if err = syscall.Listen(serverFD, maxClients); err != nil {
		return err
	}

	epollFD, err := unix.EpollCreate1(0)

	if err != nil {
		log.Fatal(err)
	}

	defer syscall.Close(epollFD)

	var socketServerEvent unix.EpollEvent = unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(serverFD),
	}

	if err = unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, serverFD, &socketServerEvent); err != nil {
		return err
	}

	log.Println("server started")

	for {

		core.RunCleanup()
		nevents, err := unix.EpollWait(epollFD, events[:], -1)

		if err != nil {
			continue
		}

		for i := 0; i < nevents; i++ {
			
			if int(events[i].Fd) == serverFD {
				fd, _, err := syscall.Accept(serverFD)
				if err != nil {
					log.Fatal("error: ", err)
					continue
				}

				clients++
				log.Println("connected clients: ", clients);
				syscall.SetNonblock(fd, true)

				var socketClientEvent unix.EpollEvent = unix.EpollEvent{
					Events: unix.EPOLLIN,
					Fd:    int32(fd),
				}

				if err := unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, fd, &socketClientEvent); err != nil {
					log.Fatal(err)
				}

			} else {
				comm := core.FDComm{Fd: int(events[i].Fd)}

				cmds, err := readCommands(comm)
				if err != nil {
					syscall.Close(int(events[i].Fd))
					clients -= 1
					log.Println("connected clients: ", clients);

					continue
				}
				if len(cmds) == 0 {
					continue
				}

				for _, cmd := range cmds {
					response, err := core.EvalCommand(cmd)
					if err != nil {
						repondError(err, comm)
						continue
					}
					if _, err := comm.Write(response); err != nil {
						log.Println("write error:", err)
						break
					}
				}

			}
		}
	}

}

func runSyncServer(conn net.Conn) {
	defer conn.Close()

	for {
		cmds, err := readCommands(conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Println(err)
			break
		}
		if len(cmds) == 0 {
			continue
		}

		for _, cmd := range cmds {
			response, err := core.EvalCommand(cmd)
			if err != nil {
				repondError(err, conn)
				continue
			}
			if _, err := conn.Write(response); err != nil {
				log.Printf("write error to %s: %v", conn.RemoteAddr().String(), err)
				return
			}
		}
	}
}

func readCommands(conn io.ReadWriter) ([]*models.RedishCmd, error) {

	var buf []byte = make([]byte, 512)
	n, err := conn.Read(buf[:])
	if err != nil {
		return nil, err
	}

	data := buf[:n]
	cmds := make([]*models.RedishCmd, 0)
	for len(data) > 0 {
		value, consumed, err := core.DecodeOne(data)
		if err != nil {
			return nil, err
		}

		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			return nil, fmt.Errorf("RESP command must be a non-empty array")
		}

		tokens := make([]string, 0, len(items))
		for _, item := range items {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("array item is not a string: %T", item)
			}
			tokens = append(tokens, str)
		}

		cmds = append(cmds, &models.RedishCmd{
			Cmd:  strings.ToUpper(tokens[0]),
			Args: tokens[1:],
		})
		data = data[consumed:]
	}
	return cmds, nil
}

func repondError(err error, conn io.ReadWriter) {
	_, _ = conn.Write([]byte(fmt.Sprintf("-%s\r\n", err)))
}
