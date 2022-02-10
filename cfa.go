package main

import (
    "fmt"
    "net/http"
    "encoding/json"
    "strings"
    "net"
    "os"
    "sync"
    "time"
    log "github.com/sirupsen/logrus"
    uj "github.com/nanoscopic/ujsonin/v2/mod"
    "go.nanomsg.org/mangos/v3"
    nanoPair   "go.nanomsg.org/mangos/v3/protocol/pair"
    nanoReq    "go.nanomsg.org/mangos/v3/protocol/req"
    "go.nanomsg.org/mangos/v3/protocol"
    //nanoRep  "go.nanomsg.org/mangos/v3/protocol/rep"
)

type CFA struct {
    udid          string
    devTracker    *DeviceTracker
    dev           *Device
    cfaProc       *GenericProc
    config        *Config
    base          string
    //sessionId     string
    startChan     chan int
    js2hid        map[int]int
    specialKeys   map[int]int
    transport     *http.Transport
    client        *http.Client
    nngPort       int
    nngPort2      int
    keyPort       int
    nngSocket     mangos.Socket  //Standard socket for sending CFRequest and receiving CSResponse records
    nngSocket2    mangos.Socket  //Secondary socket used for efficiently requesting screenshot data
    keySocket     mangos.Socket  //Socket for communicating text data to custom keyboard application, if available
    //disableUpdate bool
    keyActive     bool
    keyLock       *sync.Mutex
    sequenceNumber int
    //Note: this belongs here, not device.go, but it needs to be instantiated with the device... 
    //FromAgentCFResponseChan  chan CFResponse //messages from device nng socket, directed to this device
}

func NewCFA( config *Config, devTracker *DeviceTracker, dev *Device ) (*CFA) {
    self := NewCFANoStart( config, devTracker, dev )
    if config.cfaMethod != "manual" {
        self.start( nil )
    } else {
        self.startCfaNng( func( err int, stopChan chan bool ) {
            if err != 0 {
                dev.EventCh <- DevEvent{ action: DEV_CFA_START_ERR }
            } else {
                dev.EventCh <- DevEvent{ action: DEV_CFA_START }
            }
        } )
    }
    return self
}

func addrange( amap map[int]int, from1 int, to1 int, from2 int ) {
    for i:=from1; i<=to1; i++ {
        amap[ i ] = i - from1 + from2
    }
}

func NewCFANoStart( config *Config, devTracker *DeviceTracker, dev *Device ) (*CFA) {
    jh := make( map[int]int )  
    special := make( map[int]int )  
    //devConfig := dev.devConfig
    
    self := CFA{
        udid:          dev.udid,
        nngPort:       dev.cfaNngPort,
        nngPort2:      dev.cfaNngPort2,
        keyPort:       dev.keyPort,
        devTracker:    devTracker,
        dev:           dev,
        config:        config,
        //base:          fmt.Sprintf("http://127.0.0.1:%d",dev.wdaPort),
        js2hid:        jh,
        specialKeys:   special,
        transport:     &http.Transport{},
        keyActive:     false,
        keyLock:       &sync.Mutex{},
        sequenceNumber: 1,
        //FromAgentCFResponseChan:  make( chan CFResponse ),
    }
    //self.client = &http.Client{
    //    Transport: self.transport,
    //}
    
    /*
    The following generates a map of "JS keycodes" to Apple IO Hid event numbers.
    At least, for everything accessible without using Shift...
    At least for US keyboards.
    
    TODO: Modify this to read in information from configuration and also pay attention
    to the region of the device. In other regions keyboards will have different keys.
    
    The positive numbers here are the normal character set codes; in this case they are
    ASCII.
    
    The negative numbers are JS keyCodes for non-printable characters.
    */
/*
    addrange( jh, 97, 122, 4 ) // a-z
    addrange( jh, 49, 57, 0x1e ) // 1-9
    jh[32] = 0x2c // space
    jh[39] = 0x34 // '
    jh[44] = 0x36 // ,
    jh[45] = 0x2d // -
    jh[46] = 0x37 // .
    jh[47] = 0x38 // /
    jh[48] = 0x27 // 0
    jh[59] = 0x33 // ;
    jh[61] = 0x2e // =
    jh[91] = 0x2f // [
    jh[92] = 0x31 // \
    jh[93] = 0x30 // ]
    //jh[96] = // `
*/
    
    jh[-8] = 0x2a // backspace
    special[-8] = 0x2a
    
    jh[-9] = 0x2b // tab
    jh[-13] = 0x28 // enter
    jh[-27] = 0x29 // esc
    jh[-33] = 0x4b // pageup
    jh[-34] = 0x4e // pagedown
    jh[-35] = 0x4d // end
    jh[-36] = 0x4a // home
    
    jh[-37] = 0x50 // left
    special[-37] = 0x50
    jh[-38] = 0x52 // up
    special[-38] = 0x52
    jh[-39] = 0x4f // right
    special[-39] = 0x4f
    jh[-40] = 0x51 // down
    special[-40] = 0x51
    jh[-46] = 0x4c // delete
    special[-46] = 0x4c
      
    return &self
}
//TODO: add socket type param
func (self *CFA) dialNng( NewSocket func()(protocol.Socket, error), port int ) ( mangos.Socket, int, chan bool ) {
    spec := fmt.Sprintf( "tcp://127.0.0.1:%d", port )
    
    log.WithFields( log.Fields{
        "type":     "socket_new",
        "zmq_spec": spec,
    } ).Info("Opening nng socket")
    var err error
    var sock mangos.Socket
    
    if sock, err = NewSocket(); err != nil {
        log.WithFields( log.Fields{
            "type":     "err_socket_new",
            "zmq_spec": spec,
            "err":      err,
        } ).Info("Socket new error")
        return nil, 1, nil
    }
    
    if err = sock.Dial( spec ); err != nil {
        log.WithFields( log.Fields{
            "type": "err_socket_dial",
            "spec": spec,
            "err":  err,
        } ).Info("Socket dial error1")
        return nil, 2, nil
    }
    
    stopChan := make( chan bool )
    
    sock.SetPipeEventHook( func( action mangos.PipeEvent, pipe mangos.Pipe ) {
        //fmt.Printf("Pipe action %d\n", action )
        if action == 2 {
            stopChan <- true
        }
    } )
    
    return sock, 0, stopChan
}

/*func (self *CFA) listenNng( port int, timeoutMs int ) ( mangos.Socket, int ) {
    spec := fmt.Sprintf( "tcp://127.0.0.1:%d", port )
    
    var err error
    var repSock mangos.Socket
    
    if repSock, err = nanoRep.NewSocket(); err != nil {
        log.WithFields( log.Fields{
            "type":     "err_socket_new",
            "zmq_spec": spec,
            "err":      err,
        } ).Info("Socket new error")
        return nil, 1
    }
    
    repSock.SetOption( mangos.OptionRecvDeadline, time.Duration( timeoutMs ) * time.Millisecond )
    
    if err = repSock.Listen( spec ); err != nil {
        log.WithFields( log.Fields{
            "type": "err_socket_listen",
            "spec": spec,
            "err":  err,
        } ).Info("Socket listen error")
        return nil, 2
    }
    
    return repSock, 0
}*/

func (self *CFA) startCFResponseListener(){
    //This block sets up a listener thread which will replies FROM the iOS device
    //Messages are queued up in CFResponseChan 
    //TODO: detect shutdown
    go func () {
        log.Info("cfa: New Thread: read cfagent replies from nngSocket, feed FromAgentCFResponseChan. ", self.udid)
        for {
            msg, err := self.nngSocket.RecvMsg()
            if err != nil {
                fmt.Printf("Error receiving message %v",err)
                time.Sleep( time.Second * 2 )
                continue
            }
            cfresponse,err := NewCFResponseFromJSON(msg.Body)
            if err != nil {
                fmt.Printf("Error decoding cfresponse %v\n\n%s\n\n",err,string(msg.Body))
//                    time.Sleep( time.Second * 2 ) 
                continue
            }
            fmt.Println("XXX QUEUING Message")
            self.dev.FromAgentCFResponseChan <- cfresponse
            fmt.Println("XXX QUEUED Message")
        }
    }()
}

func (self *CFA) startCfaNng( onready func( int, chan bool ) ) {
    fmt.Println("ZZZZZZ1")
    pairs := []TunPair{
        TunPair{ from: self.nngPort, to: 8101 },
        TunPair{ from: self.nngPort2, to: 8102 },
    }
    
    if self.keyPort != 0 {
        pairs = append( pairs,
            TunPair{ from: self.keyPort, to: 8087 },
        )
    }
    
    self.dev.bridge.tunnel( pairs, func() {
        //TODO: rename nngSocket
        nngSocket, err, stopChan := self.dialNng( nanoPair.NewSocket, self.nngPort )
        if err != 0 {
            onready( err, nil )
            return
        }

        self.nngSocket = nngSocket

        self.startCFResponseListener()

        nngSocket2, err, _ := self.dialNng( nanoReq.NewSocket, self.nngPort2 )
        if err != 0 {
            onready( err, nil )
            return
        }
        self.nngSocket2 = nngSocket2
        
        if self.keyPort != 0 {
            /*success := */self.keyConnectTest()
            //if !success { onready( err, nil ) }
        }
        
        if onready != nil {
            onready( 0, stopChan )            
        }
    } )
}

func (self *CFA) start( started func( int, chan bool ) ) {
    pairs := []TunPair{
        TunPair{ from: self.nngPort, to: 8101 },
        TunPair{ from: self.nngPort2, to: 8102 },
    }
    
    if self.keyPort != 0 {
        pairs = append( pairs,
            TunPair{ from: self.keyPort, to: 8087 },
        )
    }

    self.dev.bridge.tunnel( pairs, func() {
        self.dev.bridge.cfa(
            func() { // onStart
                log.WithFields( log.Fields{
                    "type": "cfa_start",
                    "udid":  censorUuid(self.udid),
                    "nngPort": self.nngPort,
                } ).Info("[CFA] successfully started")
                
                log.WithFields( log.Fields{
                    "type": "cfa_nng_dialing",
                    "port": self.nngPort,
                } ).Debug("CFA - Dialing NNG")
                
                nngSocket, err1, stopChan := self.dialNng(nanoPair.NewSocket,self.nngPort)
                if err1 == 0 {
                    self.nngSocket = nngSocket
                    log.WithFields( log.Fields{
                        "type": "cfa_nng_dialed",
                        "port": self.nngPort,
                    } ).Debug("CFA - NNG Dialed")
                }
                self.nngSocket = nngSocket
                self.startCFResponseListener()
                
                nngSocket, err2, stopChan := self.dialNng(nanoReq.NewSocket,self.nngPort2)
                if err2 == 0 {
                    self.nngSocket2 = nngSocket
                    log.WithFields( log.Fields{
                        "type": "cfa_nng2_dialed",
                        "port": self.nngPort2,
                    } ).Debug("CFA - NNG2 Dialed")
                } else {
                    log.WithFields( log.Fields{
                        "type": "cfa_nng2_dial_fail",
                        "port": self.nngPort2,
                    } ).Debug("CFA - NNG2 Dial Failed")
                }
                self.nngSocket2 = nngSocket
                if self.keyPort != 0 {
                    self.keyConnectTest()
                }
                
                if err1 != 0 || err2 != 0 {
                    fmt.Printf("StartCFA():Error starting/connecting to CFA.\n")
                    self.dev.EventCh <- DevEvent{ action: DEV_CFA_START_ERR }
                    return
                }
                
                if started != nil {
                    started( 0, stopChan )
                }
                
                if self.startChan != nil {
                    self.startChan <- 0
                }
                
                self.dev.EventCh <- DevEvent{ action: DEV_CFA_START }
            },
            func(interface{}) { // onStop
                self.dev.EventCh <- DevEvent{ action: DEV_CFA_STOP }
            },
        )
    } )
}

/*func (self *CFA) keyListen2() bool {
    keySocket, err := self.listenNng( self.keyPort2, 10 )
    if err == 0 {
        self.keySocket2 = keySocket
        log.WithFields( log.Fields{
            "type": "key_nng_listened",
            "port": self.keyPort2,
        } ).Info("Key - NNG Listened")
        self.done = false
        self.keyListen2()
        
        // Attempt to connect to keyboard in case it is already active
        
        return true
    } else {
        log.WithFields( log.Fields{
            "type": "key_nng_listen_fail",
            "port": self.keyPort2,
        } ).Error("Key - NNG Listen Fail")
    }
    return false
}*/

func (self *CFA) keyStop() {
    self.keyActive = false
}

func (self *CFA) keyConnectTest() {
    go func() {
        log.Info("cfa: New Thread: Keyboard connection test ", self.udid)
        conn, err := net.Dial("tcp", fmt.Sprintf( ":%d", self.keyPort) )
        if err != nil {
            log.Info("cfa: Terminating Thread: Keyboard connection test. Connection failed. ", self.udid)
            log.WithFields( log.Fields{
                "type": "key_nng_connect_test_fail",
                "port": self.keyPort,
            } ).Error("Key - NNG Connect TestFail")
            return
        }
        conn.Close()
        log.WithFields( log.Fields{
            "type": "key_nng_connect_test_pass",
            "port": self.keyPort,
        } ).Info("Key - NNG Connect Test Pass")
        self.keyConnect()
        log.Info("cfa: Terminating Thread: Keyboard connection test. Connection available. Connecting...", self.udid)
    }()
}

func (self *CFA) keyConnect() {
    go func() {
        if self.keyActive { return }
        log.Info("cfa: New Thread: Keyboard connecting", self.udid)
        
        keySocket, err, _ := self.dialNng( nanoReq.NewSocket,self.keyPort )
        if err == 0 {
            self.keySocket = keySocket
            log.WithFields( log.Fields{
                "type": "key_nng_connect",
                "port": self.keyPort,
            } ).Info("Key - NNG Connected")
            self.keyActive = true
            self.keyHealthPing()
            log.Info("cfa: Terminate Thread: Keyboard connected", self.udid)
            
            // Attempt to connect to keyboard in case it is already active
            
            return
        } else {
            log.Info("cfa: Terminate Thread: Keyboard connection failed.", self.udid)
            log.WithFields( log.Fields{
                "type": "key_nng_connect_fail",
                "port": self.keyPort,
            } ).Error("Key - NNG Connect Fail")
        }
        return
    }()
}

func (self *CFA) keyHealthPing() {
    go func() {
        log.Info("cfa: New Thread: Monitor Keyboard Health", self.udid)
        for {
            self.keyLock.Lock()
            self.keySocket.Send( []byte("ping") )
            _, err := self.keySocket.Recv()
            self.keyLock.Unlock()
            
            /*if resp != nil {
                fmt.Printf("key ping resp:%s\n", string( resp ) )
            }*/
            
            if !self.keyActive { break }
            if err != nil {
                self.keyActive = false
                break
            }
            time.Sleep( time.Millisecond * 100 )
        }
        log.Info("cfa: New Thread: Monitor Keyboard Health Check failed. keyActive=False. ", self.udid)
        
        log.WithFields( log.Fields{
            "type": "key_nng_gone",
        } ).Info("Key - NNG Health Failed")
    }()
    
}

/*func (self *CFA) keyListen2() {
    go func() { for {
        bytes, err := self.keySocket2.Recv()
        if err != nil {
            if err == mangos.ErrRecvTimeout {
                if self.done { break }
                continue
            }
            time.Sleep( time.Millisecond * 10 )
            break
        }
        fmt.Printf("Key message: %s\n", string( bytes ) )
        self.keySocket2.Send([]byte("ok"))
    } }()
}*/

func (self *CFA) stop() {
    self.keyActive = false
    if self.cfaProc != nil {
        self.cfaProc.Kill()
        self.cfaProc = nil
    }
}

func ( self *CFA ) launchApplication( bundleID string ) (errorstr string){
    if bundleID == "" {
        return
    } else {
        log.WithFields( log.Fields{
            "type": "cfa_launch_app",
            "bi": bundleID,
        } ).Debug("CFA launching app")
    }
    
    cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFLaunchApplication,Application{BundleID:bundleID})) //self.SendCFRequestAndWait(NewLaunchApplicationRequest(bundleID))
    if cfresponse.Error != ""{
        log.Error("Failed to launch application ",bundleID,cfresponse.Error)
    }
    return ""
}

func (self *CFA) SendCFRequest( cfrequest *CFRequest ) {
    cfrequest.CFProviderRequestID = self.sequenceNumber
    fmt.Printf("Action: %s",cfrequest.Action) 
    self.sequenceNumber++
    jsonBytes, err := cfrequest.JSONBytes()
    if self.nngSocket == nil{
        log.Error("No socket?")
    }
    if err!= nil{
        log.Error("SendCFRequest JSON Error: ",err)
    }
    log.Info("Sending request ",cfrequest.Action)
    log.Info("Sending data: ",string(jsonBytes))
    self.nngSocket.Send(jsonBytes) 
    log.Info("Sent request ",cfrequest.Action)
}
// Responses will come back from the device in sequential order
// There could be errors, or other unexpected feedback from previous requests
// which we might need to forward along manually before we get to the
// response we expect.
// Note: do not call this function unless you are 100% guaranteed a response
// otherwise this will hang.  TODO: timeout?
func (self *CFA) GetCFResponse( cfrequest *CFRequest) *CFResponse  {
    for{
fmt.Println("Waiting for response")
        cfresponse := <- self.dev.FromAgentCFResponseChan
fmt.Printf("Got response %d %d\n",cfrequest.CFProviderRequestID,cfresponse.CFProviderRequestID)
        if cfrequest.CFProviderRequestID == cfresponse.CFProviderRequestID{
fmt.Println("Returning response")
            return cfresponse
        }
fmt.Println("Default response handler")
        self.dev.defaultResponseHandler(cfresponse)
        //absolutely should be impossible if client is behaving appropriately
        if cfresponse.CFProviderRequestID > cfrequest.CFProviderRequestID {
            return NewErrorResponse(cfrequest,fmt.Sprintf("Did not receive response from device. Waiting for sequence number %d, saw id %d"))
        }
    }
}


func (self *CFA) SendCFRequestAndWait( cfrequest *CFRequest ) (*CFResponse){
    cfrequest.RequiresResponse=true
    self.SendCFRequest(cfrequest)
    return self.GetCFResponse(cfrequest)
}

func (self *CFA) ping( ) {
    cfping := NewCFRequest(CFPing,nil)
        self.SendCFRequest(cfping);
    start := time.Now()
    for i := 1; i<500; i++{
//        self.SendCFRequest(cfnoop);
    }
      self.GetCFResponse(cfping);
    elapsed:= time.Now().Sub(start)
    log.Info("Ping elapsed time: ",elapsed)
}

func (self *CFA) home() string {
    return self.SendCFRequestAndWait(NewCFRequest(CFSimulateHomeButton,nil)).Error
}

func (self *CFA) AT() (string) {
    json := `{
      action: "homebtn"
    }`
    self.nngSocket.Send([]byte(json))
    self.nngSocket.Recv()
    self.nngSocket.Send([]byte(json))
    self.nngSocket.Recv()
    self.nngSocket.Send([]byte(json))
    self.nngSocket.Recv()
    
    return ""
}

func (self *CFA) keysXXX( codes []int ) {
    if len( codes ) > 1 {
        self.typeText( codes )
        return
    }
    code := codes[0]
    
    /*
    Only some keys are able to be pressed via IoHid, because I cannot
    figure out how to use 'shift' accessed characters/keys through it.
    
    If someone is able to figure it out please let me know as the "ViaKeys"
    method uses the much slower [application typeType] method.
    */
    if self.config.cfaKeyMethod == "iohid" {
        if self.keyActive {
            ioCode, isSpecial := self.specialKeys[ code ]
            if isSpecial {
                self.keysViaIohid( []int{ioCode} )
            } else {
                self.typeText( codes )
            }
        } else {
            dest, ok := self.js2hid[ code ]
            if ok {
                self.keysViaIohid( []int{dest} )
            } else {
                self.typeText( codes )
            }
        }
    } else {
        self.typeText( codes )
    }
}

func (self *CFA) keysViaIohid( codes []int ) {
    for _, code := range codes {
        self.ioHid( 7, code )
    }
}

func (self *CFA) ioHid( page int, code int ) {
    json := fmt.Sprintf(`{
      action: "iohid"
      page: %d
      usage: %d
      duration: 0.05
    }`, page, code )
        
    log.Info( "sending " + json )
        
    self.nngSocket.Send([]byte(json))
    self.nngSocket.Recv()
}

type CfaText struct {
    Action string `json:"action"`
    Text string `json:"text"`
}
type CfaSpecialKey struct {
    Action string `json:"action"`
    Code string `json:"code"`
}

func (self *CFA) typeText( codes []int ) {
    strArr := []string{}

    for _, code := range codes {
        if code < 0 {
            code = -code
        }
        //fmt.Printf("code is %d\n", code )
        // GoLang encodes to utf8 by default. typeText call expects utf8 encoding
        strArr = append( strArr, fmt.Sprintf("%c", rune( code ) ) )
    }
    str := strings.Join( strArr, "" )
    
    self.text( str )
}

func (self *CFA) text( text string ) {
    if self.keyActive {
        msg := CfaText {
            Action: "insert",
            Text: text,
        }
        bytes, _ := json.Marshal( msg )
        
        log.Info( "sending to keyApp: " + string(bytes) )
        
        self.keyLock.Lock()
        self.keySocket.Send( bytes )
        self.keySocket.Recv()
        self.keyLock.Unlock()
    } else {
        msg := CfaText {
            Action: "typeText",
            Text: text,
        }
        bytes, _ := json.Marshal( msg )
        
        log.Info( "sending " + string(bytes) )
          
        self.nngSocket.Send(bytes)
        self.nngSocket.Recv()
    }
}

func ( self *CFA ) swipe( x1 float64, y1 float64, x2 float64, y2 float64, duration float64 ) {
    log.Info( "Swiping:", x1, y1, x2, y2, duration )
    self.SendCFRequestAndWait(NewCFRequest(CFSwipe,NewSimpleSwipe(x1,y1,x2,y2,duration)))  
}

func (self *CFA) hasElement( elementType string, elementID string, searchPath string, timeout float64 ) (bool){
    cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFGetElement,ElementSearch{Type:elementType,ID:elementID,SearchPath:searchPath,Timeout:timeout}))
    return cfresponse.Status!="error"
}
func (self *CFA) tapElement( elementType string, elementID string, searchPath string, timeout float64 ) (bool){
    cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFTap,ElementSearch{Type:elementType,ID:elementID,SearchPath:searchPath,Timeout:timeout}))
    return cfresponse.Status!="error"
}
func (self *CFA) tapAndHoldElement( elementType string, elementID string, searchPath string, timeout float64 ) (bool){
    cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFTapAndHold,ElementSearch{Type:elementType,ID:elementID,SearchPath:searchPath,Timeout:timeout}))
    return cfresponse.Status!="error"
}


func (self *CFA) SysElPosXXX( elType string, elName string ) (float32,float32) {
    log.Info( "sysElPos:", elName )
    
    json := fmt.Sprintf( `{
        action: "sysElPos"
        type: "%s"
        id: "%s"
    }`, elType, elName )
    
    self.nngSocket.Send([]byte(json))
    resBytes, _ := self.nngSocket.Recv()
    if string(resBytes) == "" {
        return 0,0
    }
    root, _, _ := uj.ParseFull( resBytes )
    x := root.Get("x").Float32()
    y := root.Get("y").Float32()
    return x,y
}

func (self *CFA) Notice(r *CFResponse){
    r.CFDeviceID = self.udid
    if self.dev.cf != nil{
        self.dev.cf.ToServerCFResponseChan <- r
    }
}

func (self *CFA) DismissAlerts() string {
    var previousAlert IOSAlert
    for{
        var currentAlert IOSAlert
        cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFGetAlert,nil))
        if cfresponse.Error != ""{
            return "Error calling getAlert: "+cfresponse.Error
        }
        if !cfresponse.HasValue(){
            if previousAlert.Title!=""{
                self.Notice(NewAlertDismissedNotice(self.udid,previousAlert.Title,previousAlert.Buttons[0].Description))
            }
            return ""
        }
fmt.Println("XXXX4")
        err := cfresponse.GetValue(&currentAlert)
        if err != nil{
fmt.Println("XXXX4"+string(cfresponse.RawValue))
fmt.Println("XXXX4"+err.Error())
            return err.Error()
        } 
fmt.Println("XXXX5")

        if(previousAlert.Title != ""){
            if(previousAlert.Title == currentAlert.Title && previousAlert.Description == currentAlert.Description){
                return "Failed to dismiss alert "+currentAlert.Title
            }
        }
fmt.Println("XXXX6")

        if(len(currentAlert.Buttons)>0){

            //TODO: only consider explicitly specified alerts
	    //alerts := self.config.vidAlerts
fmt.Println("XXXX7")

            button := currentAlert.Buttons[0]
            fmt.Printf("Dismissing alert \"%s\" by tapping \"%s\"",currentAlert.Title,button.Description)
            cfresponse = self.SendCFRequestAndWait(NewCFRequest(CFTap,Point{button.CenterX,button.CenterY}))
            if cfresponse.Status == "error"{
                return "Error tapping button " + button.Description
            }
        }else{
fmt.Println("XXXX8")
            return "Alert "+currentAlert.Title+" has no buttons"
        }
fmt.Println("XXXX9")
        previousAlert = currentAlert
        time.Sleep( time.Second * 1 )
    }
}

func (self *CFA) GetWindowSize() (float64,float64) {
    log.Info("GetWindowSize")
    cfresponse := self.SendCFRequestAndWait(NewCFRequest(CFGetWindowSize,nil))
    log.Info("windowSize Response")
    fmt.Printf("%v",cfresponse)
    if(cfresponse.Status=="ok"){
        var r Rectangle
        cfresponse.GetValue(&r)
        log.Info("windowSize-result:",r.Width,r.Height)
        return r.Width,r.Height
    }
    return 0,0
} 

func (self *CFA) AlertInfo() ( uj.JNode, string ) {
    self.nngSocket.Send([]byte(`{ action: "alertInfo" }`))
    jsonBytes, _ := self.nngSocket.Recv()
    fmt.Printf("alertInfo res: %s\n", string(jsonBytes) )
    root, _, _ := uj.ParseFull( jsonBytes )
    presentNode := root.Get("present")
    if presentNode == nil {
        fmt.Printf("Error reading alertInfo; got back %s\n", string(jsonBytes) )
        return nil, string(jsonBytes)
    } else {
        if presentNode.Bool() == false { return nil, string(jsonBytes) }
        return root, string(jsonBytes)
    }
}

func (self *CFA) WifiIp() string {
    self.nngSocket.Send([]byte(`{ action: "wifiIp" }`))
    srcBytes, _ := self.nngSocket.Recv()
    
    return string(srcBytes)
}

func (self *CFA) ActiveApps() string {
    cfresult := self.SendCFRequestAndWait(NewCFRequest(CFGetActiveApplicationPids,nil))
    return cfresult.StringValue()
}

func (self *CFA) Source(bundleID string, pid int, json bool) string {
    action := CFGetSource
    if json{
        action = CFGetSourceJSON
    }
    if(pid>0){ //these options are mutually exclusive
        return self.SendCFRequestAndWait(NewCFRequest(action,Process{ProcessID:pid})).StringValue()
    }else{
        return self.SendCFRequestAndWait(NewCFRequest(action,Application{BundleID:bundleID})).StringValue()
    }
}

func (self *CFA) Screenshot() []byte {
    if self.nngSocket2 == nil {
        log.Error( "Could not get screenshot via NNG2: no socket" )
        return []byte{}
    }
    self.nngSocket2.Send([]byte(`{ action: "screenshot2" }`))
    imgBytes, _ := self.nngSocket2.Recv()
    
    return imgBytes
}

func (self *CFA) ShowControlCenter() {
    ccMethod := self.dev.devConfig.controlCenterMethod
  
    fmt.Printf("Opening control center\n")  
    width, height := self.GetWindowSize()
            time.Sleep( time.Second * 1 )

    if ccMethod == "bottomUp" {
        midx := width / 2
        maxy := height - 1
        self.swipe( midx, maxy, midx, maxy - 200, 0.5 )
    } else if ccMethod == "topDown" {
        maxx := width - 1
        self.swipe( maxx, 0, maxx, 200, 0.5 )
    }    
}

func (self *CFA) swipeBack() {
    width, height := self.GetWindowSize()
    midy := height / 2
    midx := width / 2
    self.swipe( 1, midy, midx, midy, 0.1 )
}

func (self *CFA) AddRecordingToCC() {
    var errorstr string
    errorstr = self.launchApplication("com.apple.Preferences")
    if errorstr!=""{
        return
    }
    self.AppChanged("com.apple.Preferences") //TODO: shouldn't launchApplication do this automatically on cfagent?
    
    //Scroll through preferences looking for Control Center?
    i := 0
//    ccEl := ""
    found := false
    for {
//        ccEl = self.GetEl("staticText","Control Center", 1 )
//        if ccEl == "" {
        if !self.tapElement("staticText","Control Center","application",1){
            self.swipeBack()
            i++
            if i>3 {
                break
            }
            continue;
        }
        found = true
        break
    }
    //TODO: and if we didn't find it?
//    self.ElClick( ccEl )
    if !found {
        return //TODO: log error
    }
//    customizeEl := self.GetEl("staticText","Customize Controls", 2 )
//    self.ElClick( customizeEl )
    self.tapElement("staticText","Customize Controls","application", 2)
    
    //x,y,w,h := self.ElPos( addRecEl )
    //fmt.Printf("x:%d,y:%d,w:%d,h:%d\n",x,y,w,h)
    
    width, height := self.GetWindowSize()
    midy := height / 2
    midx := width / 2
    
    self.swipe( midx, midy, midx, midy-100, 0.1 )
    
//    addRecEl := self.GetEl("button","Insert Screen Recording", 2 )
//
//    self.ElClick( addRecEl )
    self.tapElement("button","Insert Screen Recording","application",2)
}

func (self *CFA) StartBroadcastStream( appName string, bid string, devConfig *CDevice ) string {
    method := devConfig.vidStartMethod
    ccRecordingMethod := devConfig.ccRecordingMethod
    
    errorstr := self.launchApplication( bid )
    if errorstr!="" {
        return errorstr
    }
    fmt.Printf("Checking for alerts\n")
    errorstr = self.DismissAlerts()
    if errorstr!="" {
        return errorstr
    }
    fmt.Printf("vidApp start method: %s\n", method )
    if method == "app" {
        fmt.Printf("Starting vidApp through the app\n")
        self.tapElement("button","Broadcast Selector","",0)
        time.Sleep( time.Second * 1 )
        self.tapElement("","Start Broadcast","system,application",0)
        //TODO.... search multiple types. Search no type? All types?
//        self.tapElement("staticText","Start Broadcast","application,system",0)
      
//        toSelector := self.GetEl( "button", "Broadcast Selector", 5 )
//        self.ElClick( toSelector )
//        
//        time.Sleep( time.Second * 1 )
//        startX, startY := self.SysElPos( "button", "Start Broadcast" )
//        if startX == 0 {
//            startBtn := self.GetEl( "staticText", "Start Broadcast", 2 )
//            if startBtn == "" {
//                startBtn = self.GetEl( "button", "Start Broadcast", 2 )
//                if startBtn == "" {
//                    fmt.Printf("Error! Could not fetch Start Broadcast button\n")
//                }
//            }
//            self.ElClick( startBtn )
//        } else {
//            self.clickAt( int( startX ), int( startY ) )
//        }
    } else if method == "controlCenter" {
        fmt.Printf("Starting vidApp through control center\n")
        time.Sleep( time.Second * 2 )
        self.ShowControlCenter()
        //self.Source()
        
        time.Sleep( time.Second * 3 )
//        srBtnX,srBtnY := self.SysElPos( "button", "Screen Recording" )
//        fmt.Printf("Selecting Screen Recording; x=%f,y=%f\n", srBtnX,srBtnY )
        if ccRecordingMethod == "longTouch" {
            //self.ElLongTouch( devEl )
            //            self.dev.longPress( int(srBtnX), int(srBtnY), 2 )
//            self.SendCFRequestAndWait(NewTapAndHoldRequest(int(srBtnX), int(srBtnY)))
            self.tapAndHoldElement("button", "Screen Recording","",0 )
        } else if ccRecordingMethod == "forceTouch" {
            //self.ElForceTouch( devEl, 1 )
            // TODO
        } else {
            fmt.Printf("ccRecordingMethod for a device must be either longTouch or forceTouch\n")
            os.Exit(0)
        }
        
        time.Sleep( time.Second * 3 )
//        appX,appY := self.SysElPos( "staticText", appName )
//        self.clickAt( int(appX), int(appY) )
        
//        startX,startY := self.SysElPos( "button", "Start Broadcast" )
//        self.clickAt( int(startX), int(startY) )
        
        self.tapElement("staticText","Start Broadcast","system",0)

        time.Sleep( time.Second * 3 )
    } else if method == "manual" {
    }
    
    self.SendCFRequest(NewCFRequest(CFShowApplicationLauncher,nil))
    time.Sleep( time.Second * 5 )
    return ""
}

func (self *CFA) AppChanged( bundleID string ) {
    self.SendCFRequest(NewCFRequest(CFUpdateApplication,Application{BundleID:bundleID}))
}
