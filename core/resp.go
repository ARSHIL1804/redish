package core

import (
	"bytes"
	"fmt"
	"strconv"
)

// Encode converts common Go values into RESP payloads.
func Encode(v any, isSimple bool) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return []byte("$-1\r\n"), nil
	case string:
		if isSimple {
			return EncodeSimpleString(string(x)), nil
		} else {
			return EncodeBulkString(x), nil
		}
	case []byte:
		return EncodeBulkString(string(x)), nil
	case int:
		return EncodeInteger(int64(x)), nil
	case int8:
		return EncodeInteger(int64(x)), nil
	case int16:
		return EncodeInteger(int64(x)), nil
	case int32:
		return EncodeInteger(int64(x)), nil
	case int64:
		return EncodeInteger(x), nil
	case uint:
		return EncodeInteger(int64(x)), nil
	case uint8:
		return EncodeInteger(int64(x)), nil
	case uint16:
		return EncodeInteger(int64(x)), nil
	case uint32:
		return EncodeInteger(int64(x)), nil
	case uint64:
		if x > uint64(^uint64(0)>>1) {
			return nil, fmt.Errorf("value out of int64 range")
		}
		return EncodeInteger(int64(x)), nil
	case error:
		return EncodeError(x.Error()), nil
	case []any:
		return EncodeArray(x), nil
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return EncodeArray(items), nil
	default:
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}

func EncodeSimpleString(s string) []byte {
	return []byte("+" + s + "\r\n")
}

func EncodeInteger(v int64) []byte {
	return []byte(":" + strconv.FormatInt(v, 10) + "\r\n")
}

func EncodeBulkString(s string) []byte {
	return []byte("$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n")
}

func EncodeArray(items []any) []byte {
	buf := make([]byte, 0)
	buf = append(buf, '*')
	buf = append(buf, strconv.Itoa(len(items))...)
	buf = append(buf, '\r', '\n')
	for _, item := range items {
		encoded, err := Encode(item, false)
		if err != nil {
			eencoded := EncodeError(err.Error())
			buf = append(buf, eencoded...)
			continue
		}
		buf = append(buf, encoded...)
	}
	return buf
}

func EncodeError(msg string) []byte {
	return []byte("-" + msg + "\r\n")
}

func Decode(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty RESP payload")
	}

	value, consumed, err := DecodeOne(data)
	if err != nil {
		return nil, err
	}
	if consumed != len(data) {
		return nil, fmt.Errorf("extra data after RESP value: %d bytes left", len(data)-consumed)
	}
	return value, nil
}

func DecodeOne(data []byte) (any, int, error) {
	switch data[0] {
	case '+':
		end := bytes.Index(data, []byte("\r\n"))
		if end < 0 {
			return nil, 0, fmt.Errorf("invalid simple string")
		}
		return string(data[1:end]), end + 2, nil

	case ':':
		end := bytes.Index(data, []byte("\r\n"))
		if end < 0 {
			return nil, 0, fmt.Errorf("invalid integer")
		}
		v, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return nil, 0, err
		}
		return v, end + 2, nil

	case '-':
		end := bytes.Index(data, []byte("\r\n"))
		if end < 0 {
			return nil, 0, fmt.Errorf("invalid error")
		}
		return nil, end + 2, fmt.Errorf(string(data[1:end]))

	case '$':
		end := bytes.Index(data, []byte("\r\n"))
		if end < 0 {
			return nil, 0, fmt.Errorf("invalid bulk string length")
		}
		length, err := strconv.Atoi(string(data[1:end]))
		if err != nil {
			return nil, 0, err
		}
		if length == -1 {
			return nil, end + 2, nil
		}
		if length < 0 {
			return nil, 0, fmt.Errorf("invalid bulk string length: %d", length)
		}
		payloadStart := end + 2
		payloadEnd := payloadStart + length
		if payloadEnd+2 > len(data) {
			return nil, 0, fmt.Errorf("incomplete bulk string payload")
		}
		if string(data[payloadEnd:payloadEnd+2]) != "\r\n" {
			return nil, 0, fmt.Errorf("bulk string missing terminal CRLF")
		}
		return string(data[payloadStart:payloadEnd]), payloadEnd + 2, nil

	case '*':
		end := bytes.Index(data, []byte("\r\n"))
		if end < 0 {
			return nil, 0, fmt.Errorf("invalid array length")
		}
		count, err := strconv.Atoi(string(data[1:end]))
		if err != nil {
			return nil, 0, err
		}
		if count == -1 {
			return nil, end + 2, nil
		}
		if count < 0 {
			return nil, 0, fmt.Errorf("invalid array length: %d", count)
		}

		items := make([]any, 0, count)
		pos := end + 2
		for i := 0; i < count; i++ {
			item, consumed, err := DecodeOne(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			items = append(items, item)
			pos += consumed
		}
		return items, pos, nil

	default:
		return nil, 0, fmt.Errorf("unsupported RESP type: %q", data[0])
	}
}

func DecodeArrayString(data []byte) ([]string, error) {
	value, consumed, err := DecodeOne(data)
	if err != nil {
		return nil, err
	}
	if consumed != len(data) {
		return nil, fmt.Errorf("extra data after RESP value: %d bytes left", len(data)-consumed)
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("decoded value is not an array")
	}

	tokens := make([]string, 0, len(items))
	for _, item := range items {
		str, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("array item is not a string: %T", item)
		}
		tokens = append(tokens, str)
	}

	return tokens, nil
}
