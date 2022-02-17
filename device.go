package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	ws "github.com/gorilla/websocket"
	uj "github.com/nanoscopic/ujsonin/v2/mod"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

const (
	VID_NONE = iota
	VID_APP
	VID_BRIDGE
	VID_WDA
	VID_CFA
	VID_ENABLE
	VID_DISABLE
	VID_END
)

const (
	DEV_STOP = iota
	DEV_CFA_START
	DEV_CFA_START_ERR
	DEV_CFA_STOP
	DEV_WDA_START
	DEV_WDA_START_ERR
	DEV_WDA_STOP
	DEV_VIDEO_START
	DEV_VIDEO_STOP
	DEV_ALERT_APPEAR
	DEV_ALERT_GONE
	DEV_APP_CHANGED
)

type Device struct {
	udid                    string
	name                    string
	lock                    *sync.Mutex
	wdaPort                 int
	wdaPortFixed            bool
	cfaNngPort              int
	cfaNngPort2             int
	keyPort                 int
	vidPort                 int
	vidControlPort          int
	vidLogPort              int
	backupVideoPort         int
	iosVersion              string
	versionParts            []int
	productType             string
	productNum              string
	vidWidth                int
	vidHeight               int
	vidMode                 int
	process                 map[string]*GenericProc
	owner                   string
	connected               bool
	CFRequestChan           chan *CFRequest //messages from server, directed to this device.  See also: CFResponseChan in cfa.go
	FromAgentCFResponseChan chan *CFResponse
	EventCh                 chan DevEvent
	BackupCh                chan BackupEvent
	CFAFrameCh              chan BackupEvent
	cfa                     *CFA
	wda                     *WDA
	cfaRunning              bool
	wdaRunning              bool
	devTracker              *DeviceTracker
	config                  *Config
	devConfig               *CDevice
	cf                      *ControlFloor
	info                    map[string]string
	vidStreamer             VideoStreamer
	appStreamStopChan       chan bool
	vidOut                  *ws.Conn
	bridge                  BridgeDev
	backupVideo             BackupVideo
	backupActive            bool
	shuttingDown            bool
	alertMode               bool
	vidUp                   bool
	restrictedApps          []string
	rtcChan                 *webrtc.DataChannel
	rtcPeer                 *webrtc.PeerConnection
	imgId                   int
	orientation             string
}

func NewDevice(config *Config, devTracker *DeviceTracker, udid string, bdev BridgeDev) *Device {
	dev := Device{
		devTracker:              devTracker,
		wdaPortFixed:            false,
		cfaNngPort:              devTracker.getPort(),
		cfaNngPort2:             devTracker.getPort(),
		vidPort:                 devTracker.getPort(),
		vidLogPort:              devTracker.getPort(),
		vidMode:                 VID_NONE,
		vidControlPort:          devTracker.getPort(),
		backupVideoPort:         devTracker.getPort(),
		backupActive:            false,
		config:                  config,
		udid:                    udid,
		lock:                    &sync.Mutex{},
		process:                 make(map[string]*GenericProc),
		cf:                      devTracker.cf,
		EventCh:                 make(chan DevEvent),
		CFRequestChan:           make(chan *CFRequest, 100), //There is only one consumer, but we allow messages to be queued while processing
		FromAgentCFResponseChan: make(chan *CFResponse, 100),
		BackupCh:                make(chan BackupEvent),
		CFAFrameCh:              make(chan BackupEvent),
		bridge:                  bdev,
		cfaRunning:              false,
		versionParts:            []int{0, 0, 0},
		restrictedApps:          getApps(udid),
		imgId:                   1,
	}
	if devConfig, ok := config.devs[udid]; ok {
		dev.devConfig = &devConfig
		if devConfig.wdaPort != 0 {
			dev.wdaPort = devConfig.wdaPort
			dev.wdaPortFixed = true
		} else {
			dev.wdaPort = devTracker.getPort()
		}

		keyMethod := devConfig.keyMethod
		if keyMethod == "base" {
			dev.keyPort = 0
		} else if keyMethod == "app" {
			dev.keyPort = devTracker.getPort()
		}

	} else {
		dev.wdaPort = devTracker.getPort()
	}
	return &dev
}

func (self *Device) isShuttingDown() bool {
	return self.shuttingDown
}

func (self *Device) releasePorts() {
	dt := self.devTracker
	if !self.wdaPortFixed {
		dt.freePort(self.wdaPort)
	}
	dt.freePort(self.cfaNngPort)
	dt.freePort(self.cfaNngPort2)
	dt.freePort(self.vidPort)
	dt.freePort(self.vidLogPort)
	dt.freePort(self.vidControlPort)
	dt.freePort(self.backupVideoPort)
	if self.keyPort != 0 {
		dt.freePort(self.keyPort)
	}
}

func (self *Device) startProc(proc *GenericProc) {
	self.lock.Lock()
	self.process[proc.name] = proc
	self.lock.Unlock()
}

func (self *Device) stopProc(procName string) {
	self.lock.Lock()
	delete(self.process, procName)
	self.lock.Unlock()
}

type BackupEvent struct {
	action int
}

type DevEvent struct {
	action int
	width  int
	height int
	data   string
}

func (self *Device) shutdown() {
	go func() { self.shutdownVidStream() }()

	go func() { self.endProcs() }()

	go func() { self.EventCh <- DevEvent{action: DEV_STOP} }()
	go func() { self.BackupCh <- BackupEvent{action: VID_END} }()

	procDup := make(map[string]*GenericProc)
	for name, proc := range self.process {
		procDup[name] = proc
		proc.end = true
	}
	for _, proc := range procDup {
		log.WithFields(log.Fields{
			"type": "shutdown_dev_proc",
			"udid": censorUuid(self.udid),
			"proc": proc.name,
			"pid":  proc.pid,
		}).Info("Shutting down " + proc.name + " process")
		//go func() { proc.Kill() }()
		death_to_proc(proc.pid)
	}
}

func (self *Device) onCfaReady() {
	self.cfaRunning = true
	self.cf.notifyCfaStarted(self.udid)
	// start video streaming

	self.forwardVidPorts(self.udid, func() {
		videoMode := self.devConfig.videoMode
		if videoMode == "app" {
			self.enableAppVideo()
		} else if videoMode == "cfagent" {
			self.enableCFAVideo()
		} else {
			// TODO error
		}

		self.startProcs2()
	})
}

func (self *Device) onWdaReady() {
	self.wdaRunning = true
	self.cf.notifyWdaStarted(self.udid, self.wdaPort)
}

//WARNING: avoid go threads within this loop, or functions called from within this loop.
//         NEVER initiate a send() or receive() from within a go thread in this loop.
//
//         There is one event_loop thread per attached device, and this thread inspectings
//         traffic in both directions.
//         Requests must be forwarded to the iOS device FIFO, and returned to a calling application
//         FIFO. As such, any processing should be done here as quickly as practical..
//
//         Both requests TO the device and responses FROM the device pass through this
//         event loop.  As a consequence, while processing, both requests and response
//         traffic halt while examining a message.  This is critical and intentional.
//         It is possible that a single incoming request (For instance, "Launch Assistive Touch")
//         can be implemented as multiple requests to the device. It is essential that these
//         requests be performed "atomically"(that is, without user-generated requests accidentally
//         being inserted in the sequence from a secondary thread).  And, the handling code must
//         be able to intercept any responses from the device, without another thread shuffling
//         them back to the server.
//
//         Most of the time this is a non-issue.  The event_loop mostly touches each request or
//         response extremely briefly, then dumps it back on the wire.  In cases where it blocks traffic,
//         it is necessary to block the traffic. IMO there is little performance left
//         to be gained by a more complicated threading/locking model. (such as a dedicated reponse-handling
//         thread)
func (self *Device) startEventLoop() {
	go func() {
		fmt.Printf("Device.go: NEW THREAD. Starting device event loop %s\n", self.udid)
	LOOP:
		for {
			select {
			case cfrequest := <-self.CFRequestChan:
				action := cfrequest.Action
				var application Application
				//not perfect, but most of them....
				if strings.Contains(action, "Application") {
					cfrequest.GetArgs(&application)
				}

				log.Info("Device.go: request received ", action)
				//TODO: particular actions to look out for?
				fmt.Printf("XX %s\n", action)
				handled := false
				var errorstr string
				if action == "showTaskSwitcher" || action == "taskSwitcher" {
					errorstr = self.ShowTaskSwitcher()
				} else if action == "shake" {
					errorstr = self.shake()
				} else if action == "showControlCenter" {
					errorstr = self.showControlCenter()
					//} else if action == "startBroadcastApp" {
					//    errorstr = self.startBroadcastApp()
				} else if action == "dismissAlerts" {
					errorstr = self.dismissAlerts()
				} else if action == "toggleAssistiveTouch" || action == "assistiveTouch" {
					errorstr = self.toggleAssistiveTouch()
				} else if action == "showAssistiveTouch" {
					errorstr = self.showAssistiveTouch()
				} else if action == "hideAssistiveTouch" {
					errorstr = self.hideAssistiveTouch()
				} else if action == "startVideoStream" || action == "startStream" {
					errorstr = self.startVideoStream(cfrequest)
				} else if action == "stopVideoStream" {
					errorstr = self.stopVideoStream()
				} else if action == "getWifiMAC" { //getWifiIP implemented in CFAgent, agent cannot determine mac
					//mac := self.getWifiMAC()
					//self.cf.ToServerCFResponseChan <- NewOkValueResponse(cfrequest,mac)
					self.cf.ToServerCFResponseChan <- NewErrorResponse(cfrequest, "NOT_IMPLEMENTED")
					handled = true
				} else if action == "killApplication" {
					errorstr = self.killApplication(application.BundleID)
				} else if action == "launchApplication" || action == "launch" {
					errorstr = self.launchApplication(application.BundleID)
				} else if action == "allowApplication" || action == "allowApp" {
					errorstr = self.allowApplication(application.BundleID)
				} else if action == "restrictApplication" || action == "restrictApp" {
					errorstr = self.restrictApplication(application.BundleID)
				} else if action == "getRestrictedApplications" || action == "listRestrictedApps" {
					self.cf.ToServerCFResponseChan <- NewOkValueResponse(cfrequest, self.restrictedApps)
					handled = true
				} else if action == "initWebrtc" {
					self.initWebrtc(cfrequest)
					handled = true
				} else {
					handled = true
					self.cfa.SendCFRequest(cfrequest)
				}
				if !handled { //TODO: we don't need to reply to all of these requests either...
					if errorstr != "" {
						self.cf.ToServerCFResponseChan <- NewErrorResponse(cfrequest, errorstr)
					} else {
						self.cf.ToServerCFResponseChan <- NewOkResponse(cfrequest)
					}
				}

			// There are two consumers of this channel. Here, we do the default action, which is to
			// forward requests back to the server that initiated them.
			//
			// The second consumer is cfa.GetCFResponse(), which reads responses until it finds the
			// expected reply to its own requests.  GetCFResponse also calls device.defaultResponseHandler()
			// on messages that do not "belong" to it.  This creates an unfortunate cyclic dependency
			// between cfa and device.... Also TODO, the line below obviously assumes CFA is already running,
			// otherwise this would crash immediately...  Perhaps an additional channel between agent and
			// device would be appropriate...
			case cfresponse := <-self.FromAgentCFResponseChan:
				self.defaultResponseHandler(cfresponse)
			case event := <-self.EventCh:
				//TODO: fix indent below.  I want to commit this change without confusing my modifications

				action := event.action
				if action == DEV_STOP { // stop event loop
					self.EventCh = nil
					break LOOP
				} else if action == DEV_CFA_START { // CFA started
					self.onCfaReady()
				} else if action == DEV_WDA_START { // CFA started
					self.onWdaReady()
				} else if action == DEV_CFA_START_ERR {
					fmt.Printf("device event loop: Error starting/connecting to CFA.\n")
					self.shutdown()
					break LOOP
				} else if action == DEV_CFA_STOP { // CFA stopped
					self.cfaRunning = false
					self.cf.notifyCfaStopped(self.udid)
				} else if action == DEV_CFA_STOP { // CFA stopped
					self.wdaRunning = false
					self.cf.notifyWdaStopped(self.udid)
				} else if action == DEV_VIDEO_START { // first video frame
					self.cf.notifyVideoStarted(self.udid)
					self.onFirstFrame(&event)
				} else if action == DEV_VIDEO_STOP {
					self.cf.notifyVideoStopped(self.udid)
				} else if action == DEV_ALERT_APPEAR {
					self.enableBackupVideo()
				} else if action == DEV_ALERT_GONE {
					self.disableBackupVideo()
				} else if action == DEV_APP_CHANGED {
					self.devAppChanged(event.data)
				}
			}
		}
		log.WithFields(log.Fields{
			"type": "dev_event_loop_stop",
			"udid": censorUuid(self.udid),
		}).Info("Stopped device event loop")
	}()

}

func (self *Device) startBackupFrameProvider() {
	go func() {
		sending := false
	LOOP:
		for {
			select {
			case ev := <-self.BackupCh:
				action := ev.action
				if action == VID_ENABLE { // begin sending backup frames
					sending = true
					fmt.Printf("backup video frame sender - enabling\n")
				} else if action == VID_DISABLE {
					sending = false
					fmt.Printf("backup video frame sender - disabling\n")
				} else if action == VID_END {
					break LOOP
				}
			default:
			}
			if sending {
				self.sendBackupFrame()
			} else {
				time.Sleep(time.Millisecond * 100)
			}
		}
	}()
}

func (self *Device) startCFAFrameProvider() {
	go func() {
		sending := false
	LOOP:
		for {
			select {
			case ev := <-self.CFAFrameCh:
				action := ev.action
				if action == VID_ENABLE {
					sending = true
					fmt.Printf("cfa frame provider - enabling\n")
				} else if action == VID_DISABLE {
					sending = false
					fmt.Printf("cfa frame provider - disabled\n")
				} else if action == VID_END {
					break LOOP
				}
			default:
			}
			if sending {
				self.sendCFAFrame()
			} else {
				time.Sleep(time.Millisecond * 100)
			}
		}
	}()
}

func (self *Device) enableDefaultVideo() {
	videoMode := self.devConfig.videoMode
	if videoMode == "app" {
		self.vidMode = VID_APP
		self.vidStreamer.forceOneFrame()
	} else if videoMode == "cfagent" {
		self.vidMode = VID_CFA
	} else {
		// TODO error
	}
}

func (self *Device) disableBackupVideo() {
	fmt.Printf("Sending vid_disable\n")
	self.BackupCh <- BackupEvent{action: VID_DISABLE}
	fmt.Printf("Sent vid_disable\n")
	self.backupActive = false
	self.enableDefaultVideo()
}

func (self *Device) enableBackupVideo() {
	fmt.Printf("Sending vid_enable\n")
	self.BackupCh <- BackupEvent{action: VID_ENABLE}
	fmt.Printf("Sent vid_enable\n")
	self.vidMode = VID_BRIDGE
	self.backupActive = true
}

func (self *Device) disableCFAVideo() {
	fmt.Printf("Sending vid_disable\n")
	self.CFAFrameCh <- BackupEvent{action: VID_DISABLE}
	fmt.Printf("Sent vid_disable\n")

	self.enableDefaultVideo()
}

func (self *Device) enableCFAVideo() {
	fmt.Printf("Sending vid_enable\n")
	self.CFAFrameCh <- BackupEvent{action: VID_ENABLE}
	fmt.Printf("Sent vid_enable\n")
	self.vidMode = VID_CFA
	self.backupActive = true
}

func (self *Device) sendBackupFrame() {
	vidOut := self.vidOut
	if vidOut != nil {
		fmt.Printf("Fetching frame - ")
		pngData := self.backupVideo.GetFrame()
		fmt.Printf("%d bytes\n", len(pngData))
		if len(pngData) > 0 {
			vidOut.WriteMessage(ws.BinaryMessage, pngData)
		}
	} else {
		time.Sleep(time.Millisecond * 100)
	}
}

func (self *Device) sendCFAFrame() {
	vidOut := self.vidOut
	if vidOut != nil || self.rtcChan != nil {
		start := time.Now().UnixMilli()
		pngData := self.cfa.Screenshot()
		end := time.Now().UnixMilli()
		diff := end - start
		if diff < 300 {
			toSleep := 300 - diff
			time.Sleep(time.Duration(toSleep) * time.Millisecond)
		}
		//fmt.Printf("%d bytes\n", len( pngData ) )
		if len(pngData) > 0 {
			if self.rtcChan != nil {
				self.sendMulti(pngData)
			} else {
				vidOut.WriteMessage(ws.BinaryMessage, pngData)
			}
		}
	} else {
		time.Sleep(time.Millisecond * 100)
	}
}

func (self *Device) getBackupFrame() ([]byte, string) {
	if self == nil {
		return []byte{}, "wtf"
	}
	if self.backupVideo == nil {
		return []byte{}, "backup video not set on device object"
	}

	pngData := self.backupVideo.GetFrame()

	return pngData, ""
}

func (self *Device) stopEventLoop() {
	self.EventCh <- DevEvent{action: DEV_STOP}
}

func (self *Device) startup() {
	self.startEventLoop()
	self.startProcs()
}

func (self *Device) startBackupVideo() {
	self.backupVideo = self.bridge.NewBackupVideo(
		self.backupVideoPort,
		func(interface{}) {}, // onStop
	)
}

func (self *Device) devAppChanged(bundleID string) {
	if self.cfa == nil {
		return
	}

	self.cfa.AppChanged(bundleID)
}

func (self *Device) startProcs() {
	// Start CFA
	self.cfa = NewCFA(self.config, self.devTracker, self)

	if self.config.cfaMethod == "manual" {
		//self.cfa.startCfaNng()
	}

	self.startBackupFrameProvider() // just the timed loop
	self.startCFAFrameProvider()
	self.backupVideo = self.bridge.NewBackupVideo(
		self.backupVideoPort,
		func(interface{}) {}, // onStop
	)

	//self.enableBackupVideo()

	self.bridge.NewSyslogMonitor(func(msg string, app string) {
		//msg := root.GetAt( 3 ).String()
		//app := root.GetAt( 1 ).String()

		//fmt.Printf("Msg:%s\n", msg )

		if app == "SpringBoard(SpringBoard)" {
			if strings.Contains(msg, "Presenting <SBUserNotificationAlert") {
				alerts := self.config.alerts

				useAlertMode := true
				if len(alerts) > 0 {
					for _, alert := range alerts {
						if strings.Contains(msg, alert.match) {
							fmt.Printf("Alert matching \"%s\" appeared. Autoresponding with \"%s\"\n",
								alert.match, alert.response)
							if self.cfaRunning {
								useAlertMode = false
								if !self.cfa.tapElement("button", alert.response, "application", 0) {
									fmt.Printf("Alert does not contain button \"%s\"\n", alert.response)
								}
								//                                btnX,btnY := self.cfa.SysElPos( "button", alert.response )
								//                                if btnX == 0 {
								//                                    fmt.Printf("Alert does not contain button \"%s\"\n", alert.response )
								//                                } else {
								//                                    self.cfa.clickAt( int(btnX),int(btnY) )
								//                                }
							}

						}
					}
				}

				if useAlertMode && self.vidUp {
					fmt.Printf("Alert appeared\n")
					if len(alerts) > 0 {
						fmt.Printf("Alert did not match any autoresponses; Msg content: %s\n", msg)
					}
					self.EventCh <- DevEvent{action: DEV_ALERT_APPEAR}
					self.alertMode = true
				}
			} else if strings.Contains(msg, "deactivate alertItem: <SBUserNotificationAlert") {
				if self.alertMode {
					self.alertMode = false
					fmt.Printf("Alert went away\n")
					self.EventCh <- DevEvent{action: DEV_ALERT_GONE}
				}
			}
		} else if app == "SpringBoard(FrontBoard)" {
			if strings.Contains(msg, "Setting process visibility to: Foreground") {
				fmt.Printf("Process vis line:%s", msg)
				appStr := "application<"
				index := strings.Index(msg, appStr)
				if index != -1 {
					after := index + len(appStr)
					left := msg[after:]
					endPos := strings.Index(left, ">")
					app := left[:endPos]
					pidStr := left[endPos+2:]
					pidEndPos := strings.Index(pidStr, "]")
					pidStr = pidStr[:pidEndPos]
					pid, _ := strconv.ParseUint(pidStr, 10, 64)
					fmt.Printf("  app - bid:%s pid:%d\n", app, pid)
					allowed := true
					for _, restrictedApp := range self.restrictedApps {
						if restrictedApp == app {
							allowed = false
						}
					}
					if allowed {
						self.EventCh <- DevEvent{action: DEV_APP_CHANGED, data: app}
					} else {
						self.bridge.Kill(pid)
					}
				}
				/*} else if strings.Contains( msg, "Setting process visibility to: Background" ) {
				  // Do something when returning to Springboard*/
			} else if strings.HasPrefix(msg, "Received active interface orientation did change") {
				// "SpringBoard(FrontBoard)[60] \u003cNotice\u003e: Received active interface orientation did change from landscapeLeft (4) to landscapeLeft"
				index := strings.Index(msg, "to")
				orientation := msg[index+3 : len(msg)-6]
				//fmt.Printf( "%s", msg )
				fmt.Printf("Interface orientated changed to %s\n", orientation)
				self.orientation = orientation

				/*time.Sleep( 500 * time.Millisecond )
				  orientation := self.cfa.getOrientation()
				  fmt.Printf( "  App orientation: %s\n", orientation )*/

				self.devTracker.cf.orientationChange(self.udid, orientation)
			}
		} else if app == "dasd" {
			if strings.HasPrefix(msg, "Foreground apps changed") {
				//fmt.Printf("App changed\n")
				//self.EventCh <- DevEvent{ action: DEV_APP_CHANGED }
			}
		} else if app == "CFAgent-Runner(CFAgentLib)" {
			if strings.HasPrefix(msg, "keyxr keyboard ready") {
				self.cfa.keyConnect()
			} else if strings.HasPrefix(msg, "keyxr keyboard vanished") {
				self.cfa.keyStop()
			}
		}
		/*else if app == "backboardd" {
		    // "Effective device orientation changed to: portrait (1)"
		    // portrait, landscapeRight, landscapeLeft, portraitUpsideDown
		    if strings.HasPrefix( msg, "Effective device orientation changed" ) {
		        fmt.Printf( "%s", msg )
		        go func() {
		            time.Sleep( 500 * time.Millisecond )
		            orientation := self.cfa.getOrientation()
		            fmt.Printf( "  App orientation: %s\n", orientation )
		            self.orientation = orientation
		            self.devTracker.cf.orientationChange( self.udid, orientation )
		        }()
		    }
		}*/
	})
}

func (self *Device) startProcs2() {
	self.appStreamStopChan = make(chan bool)

	videoMode := self.devConfig.videoMode
	if videoMode == "app" {
		self.vidStreamer = NewAppStream(
			self.appStreamStopChan,
			self.vidControlPort,
			self.vidPort,
			self.vidLogPort,
			self.udid,
			self)
		self.vidStreamer.mainLoop()
	} else if videoMode == "cfagent" {
		// Nothing todo
	} else {
		// TODO error
	}

	// Start WDA
	self.wda = NewWDA(self.config, self.devTracker, self)
}

func (self *Device) vidAppIsAlive() bool {
	vidPid := self.bridge.GetPid(self.config.vidAppExtBid)
	if vidPid != 0 {
		return true
	}
	return false
}

func (self *Device) enableAppVideo() {
	// check if video app is running
	vidPid := self.bridge.GetPid(self.config.vidAppExtBid)

	// if it is running, go ahead and use it
	/*if vidPid != 0 {
	    self.vidMode = VID_APP
	    return
	}*/

	// If it is running, kill it
	if vidPid != 0 {
		self.bridge.Kill(vidPid)

		// Kill off replayd in case it is stuck
		rp_id := self.bridge.GetPid("replayd")
		if rp_id != 0 {
			self.bridge.Kill(rp_id)
		}
	}

	// if video app is not running, check if it is installed

	bid := self.config.vidAppBidPrefix + "." + self.config.vidAppBid

	installInfo := self.bridge.AppInfo(bid)
	// if installed, start it
	if installInfo != nil {
		fmt.Printf("Attempting to start video app stream\n")
		//version := installInfo.Get("CFBundleShortVersionString").String()

		// if version != "1.1" {
		// 	fmt.Printf("Installed CF Vidstream app is version %s; must be version 1.1\n", version)
		// 	panic("Wrong vidstream version")
		// }

		self.cfa.StartBroadcastStream(self.config.vidAppName, bid, self.devConfig)
		self.vidUp = true
		self.vidMode = VID_APP
		return
	}

	// fmt.Printf("Vidstream not installed; attempting to install\n")

	// // if video app is not installed
	// // install it, then start it
	// success := self.bridge.InstallApp("vidstream.xcarchive/Products/Applications/vidstream.app")
	// if success {
	// 	self.cfa.StartBroadcastStream(self.config.vidAppName, bid, self.devConfig)
	// 	self.vidMode = VID_APP
	// 	return
	// }

	// if video app failed to start or install, just leave backup video running
}

func (self *Device) justStartBroadcast() {
	bid := self.config.vidAppBidPrefix + "." + self.config.vidAppBid
	self.cfa.StartBroadcastStream(self.config.vidAppName, bid, self.devConfig)
}

func (self *Device) startVideoStream(cfrequest *CFRequest) (errorstr string) {
	conn := self.cf.connectVidChannel(self.udid)

	imgData := self.cfa.Screenshot()
	conn.WriteMessage(ws.BinaryMessage, imgData)

	var controlChan chan int
	if self.vidStreamer != nil {
		controlChan = self.vidStreamer.getControlChan()
	}

	// Necessary so that writes to the socket fail when the connection is lost
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				conn.Close()
				break
			}
		}
	}()

	self.vidOut = conn

	imgConsumer := NewImageConsumer(func(text string, data []byte) error {
		if self.vidMode != VID_APP {
			return nil
		}
		//conn.WriteMessage( ws.TextMessage, []byte( fmt.Sprintf("{\"action\":\"normalFrame\"}") ) )
		conn.WriteMessage(ws.TextMessage, []byte(text))
		return conn.WriteMessage(ws.BinaryMessage, data)
	}, func() {
		// there are no frames to send
	})

	if self.vidStreamer != nil {
		self.vidStreamer.setImageConsumer(imgConsumer)
		fmt.Printf("Telling video stream to start1\n")
		controlChan <- 1 // start
	}
	return ""
}

func (self *Device) shutdownVidStream() {
	if self.vidOut != nil {
		self.stopVideoStream()
	}
	ext_id := self.bridge.GetPid("vidstream_ext")
	if ext_id != 0 {
		self.bridge.Kill(ext_id)
	}
}

func (self *Device) stopVideoStream() (errorstr string) {
	self.vidOut = nil
	self.cf.destroyVidChannel(self.udid)
	return ""
}

func (self *Device) forwardVidPorts(udid string, onready func()) {
	self.bridge.tunnel([]TunPair{
		TunPair{from: self.vidPort, to: 8352},
		TunPair{from: self.vidControlPort, to: 8351},
		TunPair{from: self.vidLogPort, to: 8353},
	}, onready)
}

func (self *Device) endProcs() {
	if self.appStreamStopChan != nil {
		self.appStreamStopChan <- true
	}
}

func (self *Device) onFirstFrame(event *DevEvent) {
	self.vidWidth = event.width
	self.vidWidth = event.height
	log.WithFields(log.Fields{
		"type":   "first_frame",
		"proc":   "ios_video_stream",
		"width":  self.vidWidth,
		"height": self.vidWidth,
		"udid":   censorUuid(self.udid),
	}).Info("Video - first frame")
}

func (self *Device) adaptToRotation(x int, y int) (int, int) {
	w := self.devConfig.uiWidth
	h := self.devConfig.uiHeight

	switch self.orientation {
	case "portrait":
	case "portraitUpsideDown":
		x, y = (w - x), (h - y)
	case "landscapeLeft":
		x, y = y, (h - x)
	case "landscapeRight":
		x, y = (w - y), x
	}
	return x, y
}

// Called from two places.
//    1. device default event_loop
//    2. cfa.GetCFResponse
// called principally from the event loop.
// this is the device's chance to do something
// with a response, before forwarding back to ControlFloor where it will get sent back
// to any listening client.
//
func (self *Device) defaultResponseHandler(cfresponse *CFResponse) {
	//TODO: Put any logging here.
	log.Info("Received response! ", cfresponse.MessageType, cfresponse.Status)
	if self.cf != nil && cfresponse.CFServerRequestID != 0 {
		self.cf.ToServerCFResponseChan <- cfresponse
	} else {
		log.Info("Message not routeable to server: ")
		jsonBytes, _ := cfresponse.JSONBytes()
		fmt.Println(string(jsonBytes))
	}
}

//func (self *Device) clickAt( x int, y int ) {
//    x,y = self.adaptToRotation( x, y )
//    self.cfa.clickAt( x, y )
//}

//func (self *Device) doubleClickAt( x int, y int ) {
//    self.cfa.doubleClickAt( x, y )
//}

//func (self *Device) mouseDown( x int, y int ) {
//    self.cfa.mouseDown( x, y )
//}

//func (self *Device) mouseUp( x int, y int ) {
//    self.cfa.mouseUp( x, y )
//}

//func (self *Device) hardPress( x int, y int ) {
//    self.cfa.hardPress( x, y )
//}

//func (self *Device) longPress( x int, y int, time float64 ) {
//    self.cfa.longPress( x, y, time )
//}

//func (self *Device) home() {
//    self.cfa.home()
//}

func findNodeWithAtt(cur uj.JNode, att string, label string) uj.JNode {
	lNode := cur.Get(att)
	if lNode != nil {
		if lNode.String() == label {
			return cur
		}
	}

	cNode := cur.Get("c")
	if cNode == nil {
		return nil
	}

	var gotIt uj.JNode
	cNode.ForEach(func(child uj.JNode) {
		res := findNodeWithAtt(child, att, label)
		if res != nil {
			gotIt = res
		}
	})
	return gotIt
}

// Assumes AssistiveTouch is enabled already
func (self *Device) showAssistiveTouchMenu(pid int) string {
	log.Warn("10 %v", pid)
	//    y := 0
	i := 0
	for {
		log.Warn("11")
		i++
		if i > 10 {
			return "AssistiveTouch icon did not appear"
			//            return 0
		}
		log.Warn("12")
		cfrequest := NewCFRequest(CFTap, ElementSearch{ID: "AssistiveTouch menu", ProcessID: pid})
		cfresponse := self.cfa.SendCFRequestAndWait(cfrequest)
		log.Warn("Called TapElement with response ", cfresponse.Status)
		if cfresponse.Status == "ok" {
			return ""
		}
		time.Sleep(time.Millisecond * 100)
	}

	return ""
}

//TODO: Re-enable, uses AppAtPoint
func (self *Device) ShowTaskSwitcher() (errorstr string) {
	//self.cfa.Siri("activate assistivetouch")
	log.Warn("1")
	//    x,y := self.cfa.GetWindowSize()
	errorstr = self.showAssistiveTouch()
	if errorstr != "" {
		return errorstr
	}
	log.Warn("2")

	_, pid := self.isAssistiveTouchEnabled()
	log.Warn("3")

	if pid == 0 {
		return "Could not find process ID for assistive touch process"
	}

	errorstr = self.showAssistiveTouchMenu(pid)
	if errorstr != "" {
		return errorstr
	}

	log.Warn("4")

	i := 0
	for {
		log.Warn("5")
		i++
		if i > 10 {
			return "Could not find multitasking button"
		}

		//Untested. Need iOS 15
		cfrequest := NewCFRequest(CFTap, ElementSearch{ID: "Multitasking", ProcessID: pid})
		cfresponse := self.cfa.SendCFRequestAndWait(cfrequest)

		if cfresponse.Status == "ok" {
			break
		}
		time.Sleep(time.Millisecond * 200)
	}

	// Todo: Wait for task switcher to actually appear
	//time.Sleep( time.Millisecond * 600 )
	//self.cfa.GetEl("other", "SBSwitcherWindow", false, 1 )
	i = 0
	for {
		i++
		if i > 20 {
			fmt.Printf("Task Switcher did not appear\n")
			return
		}
		//XCTRunnerDaemonSession unregonized selector (iOS 14), untested...
		//cfrequest := NewGetApplicationStructureAtPointRequest("","appCloseBox",x,y,0)
		//try this for kicks instead, since the position seems irrelevant. I have no idea
		cfrequest := NewCFRequest(CFGetApplicationStructure, ElementSearch{ID: "appCloseBox", ProcessID: -1})
		cfresponse := self.cfa.SendCFRequestAndWait(cfrequest)
		if cfresponse.Status == "ok" {
			break
		}
		//        centerScreenJson := self.cfa.AppAtPoint( 187, 333, true, true, true );
		//        root3, _ := uj.Parse( []byte(centerScreenJson) )
		//        closeBox := findNodeWithAtt( root3, "id", "appCloseBox" )
		//        if closeBox != nil { break }
		time.Sleep(time.Millisecond * 100)
		//fmt.Printf("Task switcher appeared\n")
	}

	self.hideAssistiveTouch()
	return ""
}

func (self *Device) shake() (errorstr string) {
	self.showAssistiveTouch()
	self.hideAssistiveTouch()
	return ""
}

func (self *Device) showControlCenter() (errorstr string) {
	self.cfa.ShowControlCenter()
	return ""
}
func (self *Device) dismissAlerts() (errorstr string) {
	return self.cfa.DismissAlerts()
}

//func (self *Device) startBroadcastApp () (errorstr string) {
//   return self.cfa.StartBroadcastApp()
//}

func (self *Device) isAssistiveTouchEnabled() (bool, int) {
	var pid int
	procs := self.bridge.ps()

	fmt.Println("QQQQQQ %v", procs)

	for _, proc := range procs {
		if proc.name == "assistivetouchd" {
			pid = int(proc.pid)
			break
		}
	}
	if pid != 0 {
		return true, pid
	}
	return false, 0
}

func (self *Device) showAssistiveTouch() (errorstr string) {
	enabled, _ := self.isAssistiveTouchEnabled()
	if !enabled {
		return self.toggleAssistiveTouch()
	}
	return ""
}

func (self *Device) hideAssistiveTouch() (errorstr string) {
	enabled, _ := self.isAssistiveTouchEnabled()
	if enabled {
		return self.toggleAssistiveTouch()
	}
	return ""
}

//TODO: error handling
func (self *Device) toggleAssistiveTouch() (errorstr string) {
	cfa := self.cfa
	self.showControlCenter()
	time.Sleep(time.Second * 2)
	if !self.cfa.tapElement("button", "Accessibility Shortcuts", "system", 0) {
		return "Did not find Accessibility Shortcuts in Control Center"
	}

	time.Sleep(time.Second * 2)

	if !self.cfa.tapElement("button", "AssistiveTouch", "system", 0) {
		return "Failed to tap AssistiveTouch button after launching Accessibility Shortcuts menu"
	}
	time.Sleep(time.Millisecond * 100)
	cfa.home()
	time.Sleep(time.Millisecond * 300)
	cfa.home()
	return ""
}

//func (self *Device) iohid( page int, code int ) {
//    self.cfa.ioHid( page, code )
//}

//func (self *Device) swipe( x1 int, y1 int, x2 int, y2 int, delayBy100 int ) {
//    delay := float64( delayBy100 ) / 100.0
//    x1,y1 = self.adaptToRotation( x1, y1 )
//    x2,y2 = self.adaptToRotation( x2, y2 )
//    self.cfa.swipe( x1, y1, x2, y2, delay )
//}

//func (self *Device) keys( keys string ) {
//    parts := strings.Split( keys, "," )
//    codes := []int{}
//    for _, key := range parts {
//        code, _ := strconv.Atoi( key )
//        //fmt.Printf("%s becomes %d\n", key, code )
//        codes = append( codes, code )
//    }
//    self.cfa.keys( codes )
//}

//func (self *Device) text( text string ) {
//    self.cfa.text( text )
//}
//func (self *Device) specialKey( code string ) {
//    self.cfa.specialKey( code )
//}

//func (self *Device) source() string {
//    return self.cfa.SourceJson()
//}

//TODO: re-enable
/*
//func (self *Device) AppAtPoint(x int, y int) string {
//    return self.cfa.AppAtPoint(x,y,false,false,false)
//}
*/
func (self *Device) getWifiMAC() string {
	info := self.bridge.info([]string{"WiFiAddress"})
	val, ok := info["WiFiAddress"]
	if ok {
		return val
	}
	return "unknown"
}
func (self *Device) killApplication(bundleID string) (errorstr string) {
	self.bridge.KillBid(bundleID)
	return ""
}

//func respondOk(cfrequest CFRequest) (*CFResponse){
//    if cfrequest.CFServerRequestID && self.cf != nil {
//        self.cf.ToServerCFResponseChan <- NewOkResponse(cfrequest)
//    }
//}
//func respondOkValue(cfrequest CFRequest, i interface{}){
//    if cfrequest.CFServerRequestID && self.cf != nil {
//        self.cf.ToServerCFResponseChan <- NewOkValueResponse(cfrequest,i)
//    }
//}

func (self *Device) launchApplication(bundleID string) (errorstr string) {
	self.bridge.Launch(bundleID)
	return ""
}

func (self *Device) restrictApplication(bundleID string) (errorstr string) {
	fmt.Printf("Restricting app %s\n", bundleID)

	exists := false
	for _, abid := range self.restrictedApps {
		if abid == bundleID {
			exists = true
		}
	}
	if exists {
		return
	}

	dbRestrictApp(self.udid, bundleID)
	self.restrictedApps = append(self.restrictedApps, bundleID)
	return ""
}

func (self *Device) allowApplication(bundleID string) (errorstr string) {
	fmt.Printf("Allowing app %s\n", bundleID)

	newList := []string{}
	exists := false
	for _, abid := range self.restrictedApps {
		if abid == bundleID {
			exists = true
		} else {
			newList = append(newList, abid)
		}
	}
	if !exists {
		return
	}

	dbAllowApp(self.udid, bundleID)
	self.restrictedApps = newList
	return ""
}

func (self *Device) onRtcMsg(msg webrtc.DataChannelMessage) {
	/*
	   fmt.Printf("Message from DataChannel '%s': '%s'\n", d.Label(), string(msg.Data))
	*/
}

func (self *Device) onRtcOpen(rtcChan *webrtc.DataChannel) {
	/*
	   sendErr := d.SendText(message)
	               if sendErr != nil {
	                   panic(sendErr)
	               }
	*/
	self.rtcChan = rtcChan
	self.startVidStreamRtc()
}

func (self *Device) onRtcExit() {
	self.rtcChan = nil
}

func (self *Device) sendMulti(data []byte) {
	chunkMax := 16000

	size := len(data)
	//dSize := size

	thisId := self.imgId
	self.imgId++

	if size == 0 {
		return
	}

	//fmt.Printf("Total len: %d\n", size )
	count := 0
	for {
		if size > chunkMax {
			size -= chunkMax
		} else {
			count++
			break
		}
		count++
	}

	size = len(data)
	start := 0
	pieceNum := 1

	//tot := 0
	for {
		partSize := 0
		if size > chunkMax {
			partSize = chunkMax
			size -= chunkMax
		} else {
			partSize = size
			size = 0
		}

		//fmt.Printf( "start: %d - part: %d - len: %d\n", start, part, size )

		piece := data[start : start+partSize]
		//tot += len( piece )

		/*info := fmt.Sprintf( "%d/%d", pieceNum, count )
		  info = info + strings.Repeat( " ", 10-len(info) )*/

		info := fmt.Sprintf("[%d,%d,%d,%d]", pieceNum, count, thisId, partSize)
		info = info + strings.Repeat(" ", 60-len(info))

		dup := make([]byte, len(piece))
		copy(dup, piece)
		dup = append(dup, []byte(info)...)
		//fmt.Printf("Sent %d/%d - %d\n", pieceNum, count, part )
		self.rtcChan.Send(dup)

		if size == 0 {
			break
		} else {
			start += partSize
		}
		pieceNum++
	}
	//fmt.Printf("%d - %d\n",len(data), tot )
}

func (self *Device) startVidStreamRtc() {
	imgData := self.cfa.Screenshot()
	//self.rtcChan.Send( imgData )
	//self.rtcChan.SendText("test")
	self.sendMulti(imgData)

	var controlChan chan int
	if self.vidStreamer != nil {
		controlChan = self.vidStreamer.getControlChan()
	}

	//self.vidOut = conn

	imgConsumer := NewImageConsumer(func(text string, data []byte) error {
		if self.vidMode != VID_APP {
			return nil
		}
		//conn.WriteMessage( ws.TextMessage, []byte( text ) )
		//return conn.WriteMessage( ws.BinaryMessage, data )
		//self.rtcChan.Send( data )
		self.sendMulti(data)
		return nil
	}, func() {
		// there are no frames to send
	})

	if self.vidStreamer != nil {
		self.vidStreamer.setImageConsumer(imgConsumer)
		fmt.Printf("Telling video stream to start2\n")
		controlChan <- 1 // start
	}
}

func (self *Device) initWebrtc(cfrequest *CFRequest) {
	var w WebRTCRequest
	err := cfrequest.GetArgs(&w)
	if err != nil || w.Offer == "" {
		panic("Write more code")
	}
	offer := w.Offer
	fmt.Printf("Running initWebrt\n")
	peer, answer := startWebRtc(
		offer,
		func(msg webrtc.DataChannelMessage) { // onMsg
			//fmt.Printf("Running initWebrt\n")
			self.onRtcMsg(msg)
		},
		func(d *webrtc.DataChannel) { // onOpen
			fmt.Printf("Running initWebrtc - onOpen\n")
			self.onRtcOpen(d)
		},
		func() { // onExit
			fmt.Printf("Running initWebrtc - onExit\n")
			self.onRtcExit()
		},
	)
	fmt.Printf("Running initWebrt - Got answer\n")
	self.rtcPeer = peer

	self.cf.ToServerCFResponseChan <- NewOkValueResponse(cfrequest, answer)
}
