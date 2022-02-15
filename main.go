package main

import (
    "fmt"
   // "bytes"
    "bufio"
    "net/http"
    "os"
    "os/signal"
    //"runtime/pprof"
    "strings"
    "strconv"
    "syscall"
    "time"
    "io/ioutil"
    log "github.com/sirupsen/logrus"
    uc "github.com/nanoscopic/uclop/mod"
    "github.com/danielpaulus/go-ios/ios"
)

func main() {
    uclop := uc.NewUclop()
    commonOpts := uc.OPTS{
        uc.OPT("-debug","Use debug log level",uc.FLAG),
        uc.OPT("-warn","Use warn log level",uc.FLAG),
        uc.OPT("-config","Config file to use",0),
        uc.OPT("-defaults","Defaults config file to use",0),
        uc.OPT("-calculated","Path to calculated JSON values",0),
        uc.OPT("-cpuprofile","Output cpu profile data",uc.FLAG),
    }
    
    idOpt := uc.OPTS{
        uc.OPT("-id","Udid of device",0),
    }
    rawOpt := uc.OPTS{
        uc.OPT("-file","path to json file",0),
        uc.OPT("-action","explicit action name. works only when no args required",0),
        uc.OPT("-bundleId","bundleId",0),
        uc.OPT("-x","x",0),
        uc.OPT("-y","y",0),
        uc.OPT("-text","text",0),
    }
    
    runOpts := append( commonOpts,
        idOpt[0],
        uc.OPT("-nosanity","Skip sanity checks",uc.FLAG),
    )
    
    uclop.AddCmd( "run", "Run ControlFloor", runMain, runOpts )
    uclop.AddCmd( "register", "Register against ControlFloor", runRegister, commonOpts )
    uclop.AddCmd( "cleanup", "Cleanup leftover processes", runCleanup, nil )

    //uclop.AddCmd( "wda",       "Just run WDA",                     runWDA,        idOpt )
    uclop.AddCmd( "cfa",       "Just run CFA",                     runCFA,        idOpt )
    uclop.AddCmd( "winsize",   "Get device window size",           runGetWindowSize, idOpt )
    uclop.AddCmd( "screenshot","Get screenshot",                   runScreenshot, idOpt )
    uclop.AddCmd( "shottest",  "Test video via screenshots",       runShotTest,   idOpt )
    uclop.AddCmd( "at",        "Activate assistiveTouch",          runAt,         idOpt );
    
    sourceOpts := append( idOpt,
        uc.OPT("-bi","Bundle ID",0),
        uc.OPT("-pid","PID",0),
    )
    uclop.AddCmd( "raw",      "Send raw file to device, print output", runRawJson,   rawOpt )
    uclop.AddCmd( "interactive", "Read from stdin, one line per JSON command", runInteractive,  runOpts )
    uclop.AddCmd( "source",    "Get device xml source",            runSource,     sourceOpts )
    uclop.AddCmd( "wifiMac",   "Get Wifi Mac address",             runWifiMac,    idOpt )
    uclop.AddCmd( "activeApps","Get pids of active apps",          runActiveApps, idOpt )
//    uclop.AddCmd( "toLauncher","Return to launcher screen",        runToLauncher, idOpt )
    uclop.AddCmd( "alertinfo", "Get alert info",                   runAlertInfo,  idOpt )
//    uclop.AddCmd( "islocked",  "Check if device screen is locked", runIsLocked,   idOpt )
//    uclop.AddCmd( "unlock",    "Unlock device screen",             runUnlock,     idOpt )
    uclop.AddCmd( "listen",    "Test listening for devices",       runListen,     commonOpts )
    uclop.AddCmd( "ping",      "Ping device",                      runPing,       idOpt )
    
//    clickButtonOpts := append( idOpt,
//        uc.OPT("-label","Button label",uc.REQ),
//        //uc.OPT("-system","System element",uc.FLAG),
//    )
//    uclop.AddCmd( "clickEl", "Click a named element", runClickEl, clickButtonOpts )
//    uclop.AddCmd( "forceTouchEl", "Force touch a named element", runForceTouchEl, clickButtonOpts )
//    uclop.AddCmd( "longTouchEl", "Long touch a named element", runLongTouchEl, clickButtonOpts )
    uclop.AddCmd( "addRec", "Add Recording to Control Center", runAddRec, idOpt )
    
    runAppOpts := append( idOpt,
        uc.OPT("-name","App name",uc.REQ),
    )
    uclop.AddCmd( "runapp", "Run named app", runRunApp, runAppOpts )
    
//    siriOpts := append( idOpt,
//        uc.OPT("-cmd","Siri command text",uc.REQ),
//    )
//    uclop.AddCmd( "siri", "Run siri", runSiri, siriOpts )
    
//    elByPidOpts := append( idOpt,
//        uc.OPT("-pid","PID",uc.REQ),
//    )
//    uclop.AddCmd( "elByPid", "Get source of pid", runElByPid, elByPidOpts )
    
    uclop.AddCmd( "vidtest", "Test backup video", runVidTest, idOpt ) 
    
    uclop.Run()
}

func goIosGetOne( udid string, onDone func( ios.DeviceEntry ) ) {
    go func() { for {
        deviceConn, err := ios.NewDeviceConnection(ios.DefaultUsbmuxdSocket)
        defer deviceConn.Close()
        if err != nil { continue }
        muxConnection := ios.NewUsbMuxConnection(deviceConn)
        
        attachedReceiver, err := muxConnection.Listen()
        if err != nil { continue }
        
        for {
            msg, err := attachedReceiver()
            if err != nil { break }
            if msg.MessageType == "Attached" {
                audid := msg.Properties.SerialNumber
                if audid == udid {
                    goIosDevice, _ := ios.GetDevice( udid )
                    fmt.Printf("Got it; id=%d\n",goIosDevice.DeviceID)
                    
                    onDone( goIosDevice )
                } else {
                    //fmt.Printf("%s != %s\n", audid, udid )
                }
            }
        }
        time.Sleep( time.Second * 10 )
    } }()
}

func cfaForDev( id string ) (*CFA,*DeviceTracker,*Device) {
    config := NewConfig( "config.json", "default.json", "calculated.json" )
    
    fmt.Println("WWWW NewDeviceTracker 3")
    tracker := NewDeviceTracker( config, false, []string{} )
    
    devs := tracker.bridge.GetDevs( config )
    dev1 := id
    if id == "" {
        dev1 = devs[0]
    }
    fmt.Printf("Dev id: %s\n", dev1)
    
    var bridgeDev BridgeDev
    if config.bridge == "go-ios" {
        bridgeDev = NewGIDev( tracker.bridge.(*GIBridge), dev1, "x", nil )
        
        /*entry := ios.DeviceEntry{
            DeviceID: 1,
            Properties: ios.DeviceProperties{
                SerialNumber: dev1,
            },
        }
        bridgeDev.SetCustom( "goIosdevice", entry )*/
        
        /*wait := make( chan bool )
        
        goIosGetOne( dev1, func( goIosDevice ios.DeviceEntry ) {
            bridgeDev.SetCustom( "goIosdevice", goIosDevice )
            wait <- true
        } )
        
        <- wait*/
    } else {
        bridgeDev = NewIIFDev( tracker.bridge.(*IIFBridge), dev1, "x", nil )
    }
    dev := NewDevice( config, tracker, dev1, bridgeDev )
    bridgeDev.SetDevice( dev )
    
    devConfig, hasDevConfig := config.devs[ dev1 ]
    if hasDevConfig {
        bridgeDev.SetConfig( &devConfig )
    }
    
    bridgeDev.setProcTracker( tracker )
    cfa := NewCFANoStart( config, tracker, dev )
    dev.cfa = cfa
    return cfa,tracker,dev
}

func vidTestForDev( id string ) (*DeviceTracker) {
    config := NewConfig( "config.json", "default.json", "calculated.json" )
    
    fmt.Println("WWWW NewDeviceTracker 4")
    tracker := NewDeviceTracker( config, false, []string{} )
    
    devs := tracker.bridge.GetDevs( config )
    dev1 := id
    if id == "" {
        dev1 = devs[0]
    }
    fmt.Printf("Dev id: %s\n", dev1)
    
    var bridgeDev BridgeDev
    if config.bridge == "go-ios" {
        bridgeDev = NewGIDev( tracker.bridge.(*GIBridge), dev1, "x", nil )
    } else {
        bridgeDev = NewIIFDev( tracker.bridge.(*IIFBridge), dev1, "x", nil )
    }
    dev := NewDevice( config, tracker, dev1, bridgeDev )
    bridgeDev.SetDevice( dev )
    
    devConfig, hasDevConfig := config.devs[ dev1 ]
    if hasDevConfig {
        bridgeDev.SetConfig( &devConfig )
    }
    
    tracker.DevMap[ dev1 ] = dev
    
    bridgeDev.setProcTracker( tracker )
    
    dev.startBackupVideo()
    
    coroHttpServer( tracker )
    
    return tracker
}

/*func runWDA( cmd *uc.Cmd ) {
    runCleanup( cmd )
    
    id := ""
    idNode := cmd.Get("-id")
    if idNode != nil {
      id = idNode.String()
    }
    
    wda,tracker,_ := wdaForDev( id )
    wda.start( nil )
 
    dotLoop( cmd, tracker )
}*/

func runCFA( cmd *uc.Cmd ) {
    runCleanup( cmd )
    
    id := ""
    idNode := cmd.Get("-id")
    if idNode != nil {
      id = idNode.String()
    }
    
    cfa,tracker,_ := cfaForDev( id )
    cfa.start( nil )
 
    dotLoop( cmd, tracker )
}

func runVidTest( cmd *uc.Cmd ) {
    runCleanup( cmd )
    
    id := ""
    idNode := cmd.Get("-id")
    if idNode != nil {
        id = idNode.String()
    }
    
    tracker := vidTestForDev( id )
    
    dotLoop( cmd, tracker )
}

func dotLoop( cmd *uc.Cmd, tracker *DeviceTracker ) {
    c := make(chan os.Signal)
    stop := make(chan bool)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <- c
        stop <- true
        tracker.shutdown()
    }()
    
    LOOP:
    for {
        select {
            case <- stop:
                break LOOP
            default:
        }
        fmt.Printf(". ")
        time.Sleep( time.Second * 1 )
    }
    
    runCleanup( cmd )
}
func quietLoop( cmd *uc.Cmd, tracker *DeviceTracker ) {
    c := make(chan os.Signal)
    stop := make(chan bool)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <- c
        stop <- true
        tracker.shutdown()
    }()
    
    exit := 0
    for {
        select {
            case <- stop:
                exit = 1
                break
            default:
        }
        if exit == 1 { break }
//        fmt.Printf(". ")
        time.Sleep( time.Second * 10 )
    }
    
    runCleanup( cmd )
}

func runGetWindowSize( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        wid, heg := cfa.GetWindowSize()
        fmt.Printf("Width: %d, Height: %d\n", wid, heg )
    } )
}

func runAddRec( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        cfa.AddRecordingToCC()
    } )
}

func cfaWrapped( cmd *uc.Cmd, appName string, doStuff func( cfa *CFA, dev *Device ) ) {
    openDbConnection()
    
    config := NewConfig( "config.json", "default.json", "calculated.json" )
  
    runCleanup( cmd )
    
    id := ""
    idNode := cmd.Get("-id")
    if idNode != nil {
        id = idNode.String()
    }
    
    if id == "" {
        fmt.Println("WWWW NewDeviceTracker 1")
        tracker := NewDeviceTracker( config, false, []string{} )
        devs := tracker.bridge.GetDevs( config )        
        if devs == nil || len(devs) == 0{
            fmt.Println("No device listed in config.json is currently available." )
            return
        }
        id = devs[0]
    }
    
    cfa,_,dev := cfaForDev( id )
    if cfa == nil || cfa.dev == nil{
        fmt.Printf("Device with id %s is currently available.",id )
        return
    }
    fmt.Printf("id:[%s]\n", id )
    devConfig := config.devs[ id ]
    fmt.Printf("%+v\n", devConfig )
    
//    if dev.tunnelMethod == ""{
//        fmt.Printf("Config for device with id %s is currently available, or device not available.",id )
//        return
//    }

    startChan := make( chan int )
    
    fmt.Printf("devCfaMethod:%s\n", devConfig.cfaMethod )
    var stopChan chan bool
    if config.cfaMethod == "manual" || devConfig.cfaMethod == "manual" {
        fmt.Printf("Manual CFA; connecting...\n")
        go func() {
            cfa.startCfaNng( func( err int, AstopChan chan bool ) {
                stopChan = AstopChan
                fmt.Printf("Manual CFA; connected; err: %d\n", err)
                startChan <- err
            } )
        }()
    } else {
        //cfa.startChan = startChan
        cfa.start( func( err int, AstopChan chan bool ) {
            stopChan = AstopChan
            startChan <- err
        } )
    }
    
    err := <- startChan
    if err != 0 {
        fmt.Printf("Could not start/connect to CFA. Exiting")
        runCleanup( cmd )
        return
    }
    
    fmt.Printf("appName = %s\n", appName)
    if appName == "" {
        fmt.Printf("Ensuring session\n")
        //cfa.ensureSession()
        fmt.Printf("Ensured session\n")
    } else {
        //cfa.create_session( appName )
    }
    fmt.Println("DOING STUFF")
    doStuff( cfa, dev )
    
    stopChan <- true
    
    dev.shutdown()
    cfa.stop()
    
    runCleanup( cmd )
}

/*
func runClickEl( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        label := cmd.Get("-label").String()
        //system := cmd.Get("-system").Bool()
        btnName := cfa.GetEl( "any", label, 5 )
        cfa.ElClick( btnName )
    } )
}
func runForceTouchEl( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        label := cmd.Get("-label").String()
        //system := cmd.Get("-system").Bool()
        btnName := cfa.GetEl( "any", label, 5 )
        cfa.ElForceTouch( btnName, 1 )
    } )
}

func runLongTouchEl( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        label := cmd.Get("-label").String()
        //system := cmd.Get("-system").Bool()
        btnName := cfa.GetEl( "any", label, 5 )
        cfa.ElLongTouch( btnName )
    } )
}
*/

func runRunApp( cmd *uc.Cmd ) {
    appName := cmd.Get("-name").String()
    cfaWrapped( cmd, appName, func( cfa *CFA, dev *Device ) {
    } )
}

func runSource( cmd *uc.Cmd ) {
    bi := cmd.Get("-bi").String()
    pidStr := cmd.Get("-pid").String()
    pid := 0
    if pidStr != "" {
        pid, _ = strconv.Atoi( pidStr ) 
    }
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        xml := cfa.Source(bi,pid,false)
        fmt.Println( xml )
    } )
}

func runScreenshot( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        bytes := cfa.Screenshot()
        //os.Stdout.Write( bytes )
        f, _ := os.Create( "test.jpg" )
        f.Write( bytes )
        fmt.Printf("Write %d bytes\n", len( bytes ) )
    } )
}

func runShotTest( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", shotServer )
}

func shotServer( cfa *CFA, dev *Device ) {
    shotClosure := func( w http.ResponseWriter, r *http.Request ) {
        shotImg( w, r, cfa )
    }
    http.HandleFunc( "/shot", shotClosure )
    http.HandleFunc( "/", shotRoot )
    http.ListenAndServe( "0.0.0.0:8081", nil )
}

func shotRoot( w http.ResponseWriter, r *http.Request ) {
    w.Write( []byte(`
    <html>
    <head>
      <script>
        var img;
        var i = 0;
        function go() {
          img = document.getElementById("img");
          setTimeout(updateImage,200);
        }
        function updateImage() {
          img.src = "/shot#" + (i++);
          setTimeout(updateImage,200);
        }
      </script>
    </head>
    <body onload="go()">
      <img id="img" src='/shot'/>
    </body>
    </html>
    `))
}

func shotImg( w http.ResponseWriter, r *http.Request, cfa *CFA ) {
    bytes := cfa.Screenshot()
    w.Header().Set("Content-Type", "image/jpeg")
    w.Header().Set("Content-Length", strconv.Itoa( len( bytes ) ) )
    w.Header().Set("Cache-Control", "no-cache, must-revalidate" )

    w.Write( bytes )
}

func runAlertInfo( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        _, json := cfa.AlertInfo()
        fmt.Println( json )
    } )
}


func runInteractive( cmd *uc.Cmd ) {
    config := common( cmd )
    
    // This seems to do nothing... what gives
    /*if config.cpuProfile {
        f, _ := os.Create("cpuprofile")
        if err == nil {
            pprof.StartCPUProfile( f )
            defer pprof.StopCPUProfile()
        }
    }*/
    
    idNode := cmd.Get("-id")
    ids := []string{}
    if idNode != nil {
        idString := idNode.String()
        if idString != "" {
            ids = strings.Split( idString, "," )
            config.idList = ids
        }
    }
    
    cleanup_procs( config )
    
    nosanity := cmd.Get("-nosanity").Bool()
    if !nosanity {
        sane := sanityChecks( config, cmd )
        if !sane {
            fmt.Printf("Sanity checks failed. Exiting\n")
            return
        }
    }
    
    fmt.Println("WWWW NewDeviceTracker 2")
    devTracker := NewDeviceTracker( config, true, ids )


    go func(){
        reader := bufio.NewReader(os.Stdin)
        //dev.startup()
        for{
  //          var cfrequest CFRequest
            dev := devTracker.getDevice("c7ba9ff8f4b3659d3e95c6beb93b209c74233319")
//            dev := devTracker.getDevice("a9686ebb84a0091436333f5d52d4adc4181e9b07")
            if dev == nil{
                fmt.Println("Waiting for device to exist")
                time.Sleep( time.Millisecond * 1000 )
                continue
            }
            if dev.cfa == nil{
                fmt.Println("Waiting for cfa to be initialized")
                time.Sleep( time.Millisecond * 1000 )
                continue
            }else if dev.cfa.nngSocket == nil {
                fmt.Println("Waiting for cfa socket to be initialized")
                time.Sleep( time.Millisecond * 1000 )
                continue
            }
            text, _ := reader.ReadString('\n')
            text = strings.TrimSpace(text)
            if text == ""{
                continue
            }
            cfrequest , error := NewExternalCFRequestFromJSON([]byte(text))
            if error != nil{ 
                fmt.Println("Interactive JSON error: %v\n",error)
            }else{
                fmt.Println("Sending request")
                dev.CFRequestChan <- cfrequest
            }
        }
    }()

    coro_sigterm( config, devTracker )
    quietLoop( cmd, devTracker )
//    coroHttpServer( devTracker )
}
func runRawJson( cmd *uc.Cmd ) {
    filename := cmd.Get("-file")
//    action := cmd.Get("-action").String()
//    bundleId := cmd.Get("-bundleId").String()
//    text := cmd.Get("-text").String()
//    x := cmd.Get("-x").String()
//    y := cmd.Get("-y").String()
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        var cfrequest *CFRequest
        if filename != nil && filename.String() != ""{
            data, error := ioutil.ReadFile(filename.String())
            if(error != nil){
                fmt.Println("%v",error)
                return
            }
            cfrequest,error = NewExternalCFRequestFromJSON([]byte(data))
            if(error != nil){
                fmt.Println("%v",error)
                return
            }
        }else{
            fmt.Println("must specify -file")
        }
        cfresponse := cfa.SendCFRequestAndWait(cfrequest)
        value:= string(cfresponse.RawValue)
        if value=="" {
        }else if value[0]=='{' {
            //TODO: properly unescape JSON in a generic way and print...
            value = strings.Replace(value,"\\n","\n",-1)
            value = strings.Replace(value,"\\\"","\"",-1)
            fmt.Println("Value: %s",value)
        }else{
            fmt.Println("Value: %s",value)
        }
    })
}


func runWifiMac( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        ip := "TODO" //dev.WifiMac()
        fmt.Println( ip )
    } )
}

func runActiveApps( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        ids := cfa.ActiveApps()
        fmt.Println( ids )
    } )
}

func runAt( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        //cfa.AT()
        
        /*cfa.Siri("is assistivetouch active")
        el := cfa.GetEl("any","AssistiveTouch",false,300)
        cfa.ElClick(el)
        cfa.home()*/
        
        /*cfa.Siri("activate assistivetouch")
        time.Sleep( time.Millisecond * 600 )
        cfa.home()*/
        dev.ShowTaskSwitcher()
    } )
}

func runPing( cmd *uc.Cmd ) {
    cfaWrapped( cmd, "", func( cfa *CFA, dev *Device ) {
        for {
            start := time.Now()
            cfa.ping();
            elapsed:= time.Now().Sub(start)
            fmt.Printf("Elapsed ping time %v\n",elapsed)
            time.Sleep( time.Second * 1 )
        }
    } )
}

func runListen( cmd *uc.Cmd ) {
    stopChan := make( chan bool )
    listenForDevices( stopChan,
        func( id string, goIosDevice ios.DeviceEntry ) {
            fmt.Printf("Connected %s\n", id )
        },
        func( id string ) {
            fmt.Printf("Disconnected %s\n", id )
        } )
    
    c := make(chan os.Signal, syscall.SIGTERM)
    signal.Notify(c, os.Interrupt)
    <-c
}

func common( cmd *uc.Cmd ) *Config {
    debug := cmd.Get("-debug").Bool()
    warn  := cmd.Get("-warn").Bool()
    
    configPath := cmd.Get("-config").String()
    if configPath == "" { configPath = "config.json" }
    
    defaultsPath := cmd.Get("-defaults").String()
    if defaultsPath == "" { defaultsPath = "default.json" }
    
    calculatedPath := cmd.Get("-calculated").String()
    if calculatedPath == "" { calculatedPath = "calculated.json" }
    
    setupLog( debug, warn )
    
    config := NewConfig( configPath, defaultsPath, calculatedPath )
    config.cpuProfile = cmd.Get("-cpuprofile").Bool()
    
    openDbConnection()
    
    return config
}

func runCleanup( *uc.Cmd ) {
    config := NewConfig( "config.json", "default.json", "calculated.json" )
    cleanup_procs( config )    
}

func runRegister( cmd *uc.Cmd ) {
    config := common( cmd )
    
    doregister( config )
}

func runMain( cmd *uc.Cmd ) {
    config := common( cmd )
    
    // This seems to do nothing... what gives
    /*if config.cpuProfile {
        f, _ := os.Create("cpuprofile")
        if err == nil {
            pprof.StartCPUProfile( f )
            defer pprof.StopCPUProfile()
        }
    }*/
    
    idNode := cmd.Get("-id")
    ids := []string{}
    if idNode != nil {
        idString := idNode.String()
        if idString != "" {
            ids = strings.Split( idString, "," )
            config.idList = ids
        }
    }
    
    cleanup_procs( config )
    
    nosanity := cmd.Get("-nosanity").Bool()
    if !nosanity {
        sane := sanityChecks( config, cmd )
        if !sane {
            fmt.Printf("Sanity checks failed. Exiting\n")
            return
        }
    }
    
    fmt.Println("WWWW NewDeviceTracker 2")
    devTracker := NewDeviceTracker( config, true, ids )
    coro_sigterm( config, devTracker )
    
    coroHttpServer( devTracker )
}

func setupLog( debug bool, warn bool ) {
    //log.SetFormatter(&log.JSONFormatter{})
    log.SetFormatter(&log.TextFormatter{
        DisableTimestamp: true,
    })
    log.SetOutput(os.Stdout)
    if debug {
        log.SetLevel( log.DebugLevel )
    } else if warn {
        log.SetLevel( log.WarnLevel )
    } else {
        log.SetLevel( log.InfoLevel )
    }
}

func censorUuid( uuid string ) (string) {
    return "***" + uuid[len(uuid)-4:]
}
