package main

import (
    "encoding/json"
    "strconv"
)

type IOSAlertButton struct{
   Description string `json:"description"`
   CenterX json.Number `json:"centerX"`
   CenterY json.Number `json:"centerY"`
}
type IOSAlert struct{
   Title string `json:"title"`
   Description string `json:"description"`
   Buttons []IOSAlertButton `json:"buttons"`
}

func NewIOSAlertFromJSONString(s string) (IOSAlert,error){
   return NewIOSAlertFromJSONBytes([]byte(s))
}
func NewIOSAlertFromJSONBytes(b []byte) (IOSAlert,error){
    var a IOSAlert
    err := json.Unmarshal([]byte(b),&a);
    return a,err
}

type Rectangle struct{
    X float64      `json:"x"`
    Y float64      `json:"y"`
    Width float64  `json:"width"`
    Height float64 `json:"height"`
}
func NewRectangleFromJSONBytes(b []byte) (Rectangle,error) {
    var r Rectangle
    err := json.Unmarshal([]byte(b),&r);
    return r,err
}
func NewRectangleFromJSONString(s string) (Rectangle, error){
    return NewRectangleFromJSONBytes([]byte(s))
}

func jn64(d float64) json.Number{
    x := strconv.FormatFloat(d, 'f', -1, 64)
    if x == "0"{
        return json.Number("")
    }
    return json.Number(x)
}

type CFResponse struct{
    Action      string `json:"action"`                //Always echo back action to simplify debugging...
    Tag         string `json:"tag,omitempty"`         //If non-blank, will be echoed back on asynchronous reply.  
    Status      string `json:"status"`                //Currently: ok,error
    Error       string `json:"error,omitempty"`                 //Error message
    Value       string `json:"value,omitempty"`       //Value returned to requester. Potentially a JSON blob needing further decoding
    OriginalRequest string `json:"originalRequest,omitempty"` //For debugging purposes

    CFDeviceID           string `json:"cfdeviceid,omitempty"`   //Currently UDID, but should change to an internally assigned if....
    CFServerRequestID     int32 `json:"cfsrid,omitempty"`       //Used to track asynchronous replies from server
    CFServerChannelID     int32 `json:"cfscid,omitempty"`         //Used to track asynchronous replies from server
    CFProviderRequestID   int32 `json:"cfprid,omitempty"`       //Used to track asynchronous replies from provider
    CFServerRequiresAck   int32 `json:"cfsack,omitempty"`       //Even ok() with no value requires ack returned to ControlFloor server
    CFProviderRequiresAck int32 `json:"cfpack,omitempty"`       //Even ok() with no value requires ack returned to ControlFloor provider
}


type CFRequest struct{
    Action      string `json:"action"`                //Required: tap, doubleTap, text, etc...
    Tag         string `json:"tag,omitempty"`         //If non-blank, will be echoed back on asynchronous reply.  If blank, replies only on error

    
    ElementHandle string `json:"elementHandle,omitempty"`   //Control Floor cached internal temporary identifier
    ElementID     string `json:"elementId,omitempty"`   //iOS element.identifier
    ElementType   string `json:"elementType,omitempty"` //(tap,click)
    ElementSearchPath string `json:"elementSearchPath,omitempty"` //"application,system","system,application","system",(default)"application"    

    Text        string `json:"text,omitempty"`        //Generic "text" parameter (typeText, etc)
    Orientation string `json:"orientation,omitempty"` //For setOrientation: portrait, portraitUpsideDown, landscapeLeft, landscapeRight
    Key         string `json:"key,omitempty"`         //TypeSpecialKey 
    Offer       string `json:"offer,omitempty"`       //WebRTC 
    
//    Value       string `json:"value,omitempty"`       //Generic "value" param for setter functions.  
//    Modifier    string `json:"modifier,omitempty"`    //envisioned as a modifier key (shift, ctrl, windows,....) for typing

    //For x*,y* values
    //nsform   string `json:",omitempty"`      //if target == "video", all x,y coords need translation based on screen rotation

    //coordinates for positional commands (tap, click, press)
    X  json.Number `json:"x,omitempty"`
    Y  json.Number `json:"y,omitempty"`

    //coordinates for motion commands (positional swipe)
    X1 json.Number `json:"x1,omitempty"`
    Y1 json.Number `json:"y1,omitempty"`
    X2 json.Number `json:"x2,omitempty"`
    Y2 json.Number `json:"y2,omitempty"`

    // tapAndHold/longPress etc.
    Duration json.Number `json:"duration,omitempty"`
    Timeout  json.Number `json:"timeout,omitempty"`

    //source() command
    BundleID string       `json:"bundleId,omitempty"`
    ProcessID json.Number `json:"processId,omitempty"`

    //ioHID
    Page  json.Number `json:"page,omitempty"`
    Usage json.Number `json:"usage,omitempty"`

    Direction string  `json:"direction,omitempty"`   //up down left right,  used for non-positional swipe
    Velocity json.Number `json:"velocity,omitempty"` //pixels per second

    //The following fields are for internal use between server and provider
    CFDeviceID           string `json:"cfdeviceid,omitempty"`          //Currently UDID, but should change to an internally assigned if....
    CFServerRequestID     int32 `json:"cfsrid,omitempty"`         //Used to track asynchronous replies from server
    CFServerChannelID     int32 `json:"cfscid,omitempty"`         //Used to track asynchronous replies from server
    CFProviderRequestID   int32 `json:"cfprid,omitempty"`         //Used to track asynchronous replies from provider
    CFServerRequiresAck   int32 `json:"cfsack,omitempty"`       //Even ok() with no value requires ack returned to ControlFloor server
    CFProviderRequiresAck int32 `json:"cfpack,omitempty"`       //Even ok() with no value requires ack returned to ControlFloor provider
    onRes func( CFResponse ) `json:"-"`      //Questionable....
}

func NewClickRequest(x float64, y float64) (*CFRequest){
    return &CFRequest{Action:"click", X:jn64(x), Y:jn64(y)}
}

func NewTapElementRequest( elementType string, elementID string, searchPath string, timeout float64 ) (*CFRequest){
    return &CFRequest{Action:"tap",ElementID:elementID,ElementType:elementType,ElementSearchPath:searchPath,Timeout:jn64(timeout)}
}
func NewGetApplicationStructureRequest( elementType string, elementID string, processID int, timeout float64 ) (*CFRequest){
    return &CFRequest{Action:"getApplicationStructureAtPoint",ElementID:elementID,ElementType:elementType,Timeout:jn64(timeout),ProcessID:json.Number(strconv.Itoa(processID))}
}
//elementType and elementID should generally be blank, but if provided a secondary search will be performed.
//in effect, nothing will be returned unless the element you are looking for is actually at the specified point
func NewGetApplicationStructureAtPointRequest( elementType string, elementID string, x float64, y float64, timeout float64 ) (*CFRequest){
    return &CFRequest{Action:"getApplicationStructureAtPoint",ElementID:elementID,ElementType:elementType,Timeout:jn64(timeout),X:jn64(x), Y:jn64(y)}
}
func NewGetElementStructureAtPointRequest( elementType string, elementID string, x float64, y float64, timeout float64 ) (*CFRequest){
    return &CFRequest{Action:"getElementStructureAtPoint",ElementID:elementID,ElementType:elementType,Timeout:jn64(timeout),X:jn64(x), Y:jn64(y)}
}
func NewTapAndHoldElementRequest( elementType string, elementID string, searchPath string, timeout float64 ) (*CFRequest){
    return &CFRequest{Action:"tapAndHold",ElementID:elementID,ElementType:elementType,Timeout:jn64(timeout)}
}


func NewSwipeRequest(x1 float64, y1 float64, x2 float64, y2 float64, duration float64) (*CFRequest){
    return &CFRequest{Action:"swipe", X1:jn64(x1), Y1:jn64(y1), X2:jn64(x2), Y2:jn64(y2),Duration:jn64(duration)}
}
//The only reason for these definitions is compile-time checks vs. runtime typo checks
func NewGetOrientationRequest()(*CFRequest){
   return &CFRequest{Action:"getOrientation"}
}
func NewSetOrientationRequest(orientation string) (*CFRequest){
   return &CFRequest{Action:"getOrientation",Orientation:orientation}
}
func NewSimulateHomeButtonRequest() (*CFRequest){
   return &CFRequest{Action:"simulateHomeButton"}
}
//Write the specified message directly to the iOS device's log file
func NewNSLogRequest(text string) (*CFRequest) {
   return &CFRequest{Action:"nslog",Text:text}
}

func NewGetWindowSizeRequest()(*CFRequest){
   return &CFRequest{Action:"getWindowSize"}
}
func NewLaunchApplicationRequest(bundleID string)(*CFRequest){
   return &CFRequest{Action:"launchApplication",BundleID:bundleID}
}
func NewTapAndHoldRequest(x float64, y float64) (*CFRequest){
   return &CFRequest{Action:"tapAndHold", X:jn64(x), Y:jn64(y)}
}

func NewOkResponse(r CFRequest)(*CFResponse){
   return &CFResponse{
       Action:r.Action,
       Tag:r.Tag,
       Status:"ok",
       CFDeviceID:r.CFDeviceID,
       CFServerRequestID:r.CFServerRequestID,
       CFServerChannelID:r.CFServerChannelID,
   }
}
func NewPongResponse(r CFRequest)(*CFResponse){
   return &CFResponse{
       Action:r.Action,
       Tag:r.Tag,
       Status:"ok",
       Value:"pong",
       CFDeviceID:r.CFDeviceID,
       CFServerRequestID:r.CFServerRequestID,
       CFServerChannelID:r.CFServerChannelID,
   }
}
func NewErrorResponse(r CFRequest, error string)(*CFResponse){
   return &CFResponse{
       Action:r.Action,
       Tag:r.Tag,
       Status:"error",
       Error:error,
       CFDeviceID:r.CFDeviceID,
       CFServerRequestID:r.CFServerRequestID,
       CFServerChannelID:r.CFServerChannelID,
   }
}
func NewOkValueResponse(r CFRequest, i interface{})(*CFResponse){
   bytes,_ := json.Marshal(i)
   return &CFResponse{
       Action:r.Action,
       Tag:r.Tag,
       Status:"ok",
       CFDeviceID:r.CFDeviceID,
       CFServerRequestID:r.CFServerRequestID,
       CFServerChannelID:r.CFServerChannelID,
       Value:string(bytes),
   }
}

func (self *CFRequest) resHandler() ( func(cfresponse CFResponse) ) {
    return self.onRes
}
func (self *CFRequest) needsResponse() (bool) { return true }

func (self *CFRequest) asText( id int16 ) (string) {
    self.CFServerRequestID = int32(id);
    bytes,_ := json.Marshal(self)
    return string(bytes)
}

//Notices are triggered from either the provider or CFAgent
//They are returned
//type CFNotice struct{
//    Event       string `json:"event"`
//    Orientation string `json:"orientation,omitempty"`
//}
//func NewOrientationNoticeResponse(deviceID string, orientation string) *CFResponse {
//    notice := CFEventNotice{Event:"orientationChanged",Orientation:orientation}
//    return NewCFEventNoticeResponse(deviceID, notice)
//}
//func NewCFEventNoticeResponse(deviceID string, event CFEventNotice) *CFResponse {
//    bytes,_ := json.Marshal(event)
//    cfresponse := CFResponse{Action:"eventNotice",Value:string(bytes),CFDeviceID:deviceID}
//    return &cfresponse
//}
func NewOrientationChangedNotice(deviceID string, orientation string) *CFResponse {
    return &CFResponse{Action:"notice",Status:"ORIENTATION_CHANGED",Value:orientation,CFDeviceID:deviceID}
}
func NewAlertDismissedNotice(deviceID string, title string, button string) *CFResponse {
    return &CFResponse{Action:"notice",Status:"ALERT_DISMISSED",Value:"Dismissed \""+title+"\". Selected \""+button+"\"",CFDeviceID:deviceID}
}


