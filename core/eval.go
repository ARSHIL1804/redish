package core


import (
	"log"
	"errors"
	"redish/core/models"
	"strconv"
	"time"
)

func evalPing(args []string) ([]byte, error){ 
	if len(args) > 1 {
	    return nil, errors.New("wrong number of arguments for 'ping' command")
	}
	var encodedRes []byte
	var err error
	if len(args) == 0 {
		log.Println("PNGING PONG")
		encodedRes, err = Encode("PONG", true)
	} else {
		encodedRes, err = Encode(args[0], false)
	}
	return encodedRes, err
}

func evalSet(args []string) ([]byte, error) {
	if len(args) < 2 {
	    return nil, errors.New("wrong number of arguments for 'set' command")
	}
	key, value := args[0], args[1]
    var exDurationMs int64 = -1
	for i := 2; i< len(args); i++ {
		switch args[i] {
		case "EX", "ex":
			i++
			if i == len(args){
	    		return nil, errors.New("wrong number of arguments for 'set' command")
			}

			exDuration, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return nil, errors.New("syntax error")
			}
			exDurationMs = exDuration * 1000

		default: 
		   return nil, errors.New("syntax error")
		}
	}
	if err := PUT(key, value, exDurationMs); err != nil {
		return nil, err
	}
	return Encode("OK", true)
}


func evalGet(args []string) ([]byte, error) { 
	if len(args) != 1 {
	    return nil, errors.New("wrong number of arguments for 'get' command")
	}

	key := args[0]

	var obj = GET(key)
	if obj == nil {
		return Encode(nil, false)
	}

	if obj.ExpiresAt != -1 && obj.ExpiresAt <= time.Now().UnixMilli(){
		return Encode(nil, false)
	}

	return Encode(obj.Value, false)

}

func evalTtl(args []string) ([]byte, error){
	if len(args) != 1 {
	    return nil, errors.New("wrong number of arguments for 'ttl' command")
	}

	key := args[0]

	var obj = GET(key)
	if obj == nil {
		return Encode(-2, false)
	}

	if obj.ExpiresAt == -1 {
		return Encode(-1, false)
	}

	expiresIn := obj.ExpiresAt - time.Now().UnixMilli()

	if expiresIn < 0 {
		return Encode(-1, false)
	}
	return Encode(expiresIn / 1000, false)
}

func evalExpire(args []string) ([]byte, error){
	if len(args) != 2 {
	    return nil, errors.New("wrong number of arguments for 'expire' command")
	}

	key := args[0]

	exDuration, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return nil, errors.New("syntax error")
	}


	var exDurationMs int64 = exDuration * 1000

	if GET(key) == nil {
		return Encode(0, false)
	}
	if !EXPIRE(key, exDurationMs) {
		return Encode(0, false)
	}

	return Encode(1, false)
}

func evalDel(args []string) ([]byte, error){
	if len(args) == 0 {
	    return nil, errors.New("wrong number of arguments for 'del' command")
	}

	var countDeleted = 0
	for _, key := range args {
		if ok := DEL(key); ok {
			countDeleted++
		}
	}
	return Encode(countDeleted, false)
}

func EvalCommand(cmd *models.RedishCmd) ([]byte, error) {
	switch cmd.Cmd {
	case "PING":
		return evalPing(cmd.Args)
	case "SET":
		return evalSet(cmd.Args)
	case "GET":
		return evalGet(cmd.Args)
	case "TTL":
		return evalTtl(cmd.Args)
	case "EXPIRE":
		return evalExpire(cmd.Args)
	case "DEL":
		return evalDel(cmd.Args)
	default:
		return evalPing(cmd.Args)
	}
}