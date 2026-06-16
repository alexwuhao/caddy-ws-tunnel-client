package proto

import "encoding/json"

const (
	TypeHello        = "hello"
	TypeWelcome      = "welcome"
	TypeConfig       = "config"
	TypeReloadConfig = "reload_config"
	TypePing         = "ping"
	TypePong         = "pong"
	TypeStats        = "stats"
	TypeDrain        = "drain"
	TypeBye          = "bye"
	TypeError        = "error"
)

const (
	RoleControl = "control"
	RoleData    = "data"
)

type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func WrapEnvelope(typ string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: typ, Payload: raw})
}

type Hello struct {
	Role      string `json:"role"`
	Node      string `json:"node"`
	Key       string `json:"key"`
	Session   string `json:"session,omitempty"`
	ChannelID int    `json:"channel_id,omitempty"`

	Ts    int64  `json:"ts,omitempty"`
	Nonce string `json:"nonce,omitempty"`
	Sig   string `json:"sig,omitempty"`
}

type Welcome struct {
	Session   string  `json:"session,omitempty"`
	Heartbeat string  `json:"heartbeat,omitempty"`
	Mux       *Mux    `json:"mux,omitempty"`
	Channels  int     `json:"channels,omitempty"`
	ChannelID *int    `json:"channel_id,omitempty"`
	Config    *Config `json:"config,omitempty"`
}

type Mux struct {
	Version          int    `json:"version,omitempty"`
	KeepAliveItvl    string `json:"keepalive_interval,omitempty"`
	KeepAliveTimeout string `json:"keepalive_timeout,omitempty"`
	MaxFrameSize     int    `json:"max_frame_size,omitempty"`
	MaxReceiveBuffer int    `json:"max_receive_buffer,omitempty"`
	MaxStreamBuffer  int    `json:"max_stream_buffer,omitempty"`
}

type Config struct {
	Ports             map[string]string `json:"ports"`
	MaxStreamsPerConn int               `json:"max_streams_per_conn,omitempty"`
	Channels          int               `json:"channels,omitempty"`
	DialTimeout       string            `json:"dial_timeout,omitempty"`
	QueueTimeout      string            `json:"queue_timeout,omitempty"`
}

type Ping struct {
	Nonce string `json:"nonce,omitempty"`
	Ts    int64  `json:"ts,omitempty"`
}

type Pong struct {
	Nonce string `json:"nonce,omitempty"`
	Ts    int64  `json:"ts,omitempty"`
}

type Stats struct {
	Channels        int   `json:"channels,omitempty"`
	Streams         int   `json:"streams,omitempty"`
	BytesIn         int64 `json:"bytes_in,omitempty"`
	BytesOut        int64 `json:"bytes_out,omitempty"`
	LocalDialErrors int64 `json:"local_dial_errors,omitempty"`
}

type Drain struct {
	Deadline string `json:"deadline,omitempty"`
}

type Bye struct {
	Reason string `json:"reason,omitempty"`
}

type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Ref  string `json:"ref,omitempty"`
}
