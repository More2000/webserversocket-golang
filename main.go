package main

import (
    "log"
    "net/http"
    "sync"
	"fmt"
    "encoding/json"
    "github.com/gorilla/websocket"
	"bytes"
	// IMPORTE ESTO PARA ARREGLAR EL INT A STRING DE TIMESTAMP

)

type JsonMessage struct {
	Event  string                 `json:"nombre"`
	Params map[string]interface{} `json:"params"`
}

type Cliente struct {
	conn       *websocket.Conn
	send       chan []byte
	server     *WebSocketServer
	eventHandlers map[string][]func(map[string]interface{})
    params        map[string]string
	mu         sync.Mutex
}

type WebSocketServer struct {
	clientes       map[*Cliente]bool
	register       chan *Cliente
	unregister     chan *Cliente
	connectHandlers []func(*Cliente)
	mu             sync.Mutex
}

func NuevoServidorWebSocket() *WebSocketServer {
	return &WebSocketServer{
		clientes:       make(map[*Cliente]bool),
		register:       make(chan *Cliente),
		unregister:     make(chan *Cliente),
		connectHandlers: []func(*Cliente){},
	}
}

func (server *WebSocketServer) Ejecutar() {
	for {
		select {
		case cliente := <-server.register:
			server.clientes[cliente] = true
			for _, handler := range server.connectHandlers {
				handler(cliente)
			}
		case cliente := <-server.unregister:
			if _, ok := server.clientes[cliente]; ok {
				delete(server.clientes, cliente)
				close(cliente.send)
			}
		}
	}
}

func (server *WebSocketServer) OnConnect(handler func(*Cliente)) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.connectHandlers = append(server.connectHandlers, handler)
}

func (server *WebSocketServer) ManejarConexiones(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error al actualizar la conexión:", err)
		return
	}

    queryParams := r.URL.Query()
    params := make(map[string]string)
	for key := range queryParams {
		params[key] = queryParams.Get(key)
	}

	cliente := &Cliente{conn: conn, send: make(chan []byte, 256), server: server, eventHandlers: make(map[string][]func(map[string]interface{})), params: params}
	server.register <- cliente

	go cliente.escribirMensajes()
	cliente.leerMensajes()
}

func (c *Cliente) On(event string, handler func(map[string]interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eventHandlers[event] = append(c.eventHandlers[event], handler)
}

func (c *Cliente) Emit(event string, params map[string]interface{}) {
	message := JsonMessage{
		Event:  event,
		Params: params,
	}
	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error encoding JSON: %s", err)
		return
	}
	c.send <- messageBytes
}

func (c *Cliente) leerMensajes() {
	defer func() {
		c.server.unregister <- c
		c.conn.Close()
	}()
	for {
		_, mensaje, err := c.conn.ReadMessage()
		if err != nil {
			log.Println("Error leyendo el mensaje:", err)
			break
		}
		var data JsonMessage

		errJson := json.Unmarshal(mensaje, &data)
		if errJson != nil {
			log.Printf("Error decoding JSON: %s", errJson)
			continue
		}

		log.Printf("Event: %s, Params: %v\n", data.Event, data.Params)

		if handlers, ok := c.eventHandlers[data.Event]; ok {
			for _, handler := range handlers {
				handler(data.Params)
			}
		}
	}
}

func (c *Cliente) escribirMensajes() {
	for mensaje := range c.send {
		c.conn.WriteMessage(websocket.TextMessage, mensaje)
	}
}


func SocketFeli(mux *http.ServeMux) *WebSocketServer {
	server := NuevoServidorWebSocket()

	handler := func(w http.ResponseWriter, r *http.Request) {
		server.ManejarConexiones(w, r)
	}
	mux.HandleFunc("/socket.feli", handler)
	return server
}

func main() {

    mux := http.NewServeMux()

    server := SocketFeli(mux)

    server.OnConnect(func(cliente *Cliente) {
		log.Println("nuevo cliente conectado")
		log.Println("Params:")
		log.Println(cliente.params)

		cliente.On("temperatura", func(params map[string]interface{}) {
			log.Println("Evento temperatura params:", params)
			log.Println("temperatura:", params["temperatura"])

			temperatura, ok := params["temperatura"].(string)
			if !ok {
				fmt.Println("Error: temperatura no es una cadena")
				return
			}
			timestamp, ok := params["timestamp"].(string)
			if !ok {
				fmt.Println("Error: timestamp no es un numero")
				return
			}



			url := "http://localhost:5000/webhook"
			data := map[string]string{
				"temperatura":  temperatura,
				// AGREGUÉ "strconv.Itoa" para convertir un INT a string
				"timestamp": timestamp,
			}

			JSON, err := json.Marshal(data)
			if err != nil {
				fmt.Println("Error convirtiendo a JSON:")
				fmt.Println(err)
				return
			}

			req, err := http.NewRequest("POST", url, bytes.NewBuffer(JSON))
			if err != nil {
				fmt.Println("Error creando el request:")
				fmt.Println(err)
				return
			}

			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				fmt.Println("Error al enviar al webhoock")
				fmt.Println( err)
				return
			}
			defer resp.Body.Close()


			cliente.Emit("change_temperatura", map[string]interface{}{"temperatura": params["temperatura"], "timestamp": params["timestamp"]})
		})
	})

    go server.Ejecutar()
    
    log.Println("Server started on :8000")
    http.ListenAndServe(":8000", mux)

}