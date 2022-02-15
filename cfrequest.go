package main

import (
    "encoding/json"
    "fmt"
)

// These exist to let the compiler catch typos
// In general, NewCFRequest() should take a command from this list
// If you make a new command, and need to execute it from go, add it 
// to this list.
const (
  CFGetActiveApplicationPids = "getActiveApplicationPids"
  CFGetAlert                 = "getAlert"
  CFGetApplicationStructure  = "getActiveApplicationStructure"
  CFGetElement               = "getElement"
  CFGetOrientation           = "getOrientation"
  CFGetSource                = "getSource"
  CFGetSourceJSON            = "getSourceJSON"
  CFGetWindowSize            = "getWindowSize"
  CFLaunchApplication        = "launchApplication"    
  CFNoOp                     = "noOp"
  CFPing                     = "ping"
  CFShowApplicationLauncher  = "showApplicationLauncher"    
  CFSimulateHomeButton       = "simulateHomeButton"    
  CFStartVideoStream         = "startVideoStream"
  CFStopVideoStream          = "stopVideoStream"
  CFSwipe                    = "swipe"
  CFTap                      = "tap"
  CFTapAndHold               = "tapAndHold"
  CFUpdateApplication        = "updateApplication"    
)

type CFRoutingData struct{
    CFDeviceID         string `json:"d,omitempty"`         //Currently UDID, but should change to an internally assigned if....
    CFServerRequestID     int `json:"s,omitempty"`         //Used to track asynchronous replies from server
    CFServerChannelID     int `json:"c,omitempty"`         //Used to track asynchronous replies from server
    CFProviderRequestID   int `json:"p,omitempty"`         //Used to track asynchronous replies from provider
}

type CFRequest struct{
    CFRoutingData                    `json:"cf,omitempty"`            //Control floor agent will echo anything under the 'cf' tag
    Action           string          `json:"action"`        //Required: tap, doubleTap, text, etc...
    Tag              json.RawMessage `json:"tag,omitempty"` //If non-blank, will be echoed back on reply
    RequiresResponse IntBool         `json:"ack,omitempty"` //If 1, ack required, else optional. Can be set to one by the customer, 
                                                            //the server, or the provider anywhere along the way, and CFAgent is always free
                                                            //to send a response. 
    Args             json.RawMessage `json:"args,omitempty"`
    //The following fields are for internal use between server and provider
    onRes func( CFResponse ) `json:"-"`      //Questionable....
}

func NewCFRequest(action string,data interface{}) *CFRequest{
    cfrequest := CFRequest{Action:action}
    var err error
    if data!=nil{
        cfrequest.Args,err = json.Marshal(data)
        if(err!=nil){
            panic("Error encoding internally generated JSON?!")
        } 
    }
    return &cfrequest
}

//Internal requests (to/from server, provider, or agent preserve all Routing data as-is)
//Also, they are presumed to be well-formatted, hence panic() on error
func NewInternalCFRequestFromJSON(b []byte) (*CFRequest,error){
    var r CFRequest;
    err:= json.Unmarshal(b,&r)
    if(err!=nil){
        return nil,err
    }
    return &r,nil
}
//from customer/web client/command line/etc.
//no initial Routing data allowed, more permissive validation perhaps
//also, allowed to fail on invalid input, caller must handle
func NewExternalCFRequestFromJSON(b []byte) (*CFRequest,error){
    var r CFRequest;
    err:= json.Unmarshal(b,&r)
    if(err!=nil){
        r.ClearRoutingData() //just in case customer defines this, clobber it
        return nil, err
    }
    return &r,nil
}
func (self *CFRequest) JSONBytes() ([]byte,error){
    return json.Marshal(self)
}
//Pass in a *struct representing the data you expect to receive.  
func (self *CFRequest) GetArgs(d interface{}) error{
    return json.Unmarshal(self.Args,d)
}
func (self *CFRequest) ClearRoutingData(){
    self.CFRoutingData = CFRoutingData{}
}



type CFResponse struct{
    CFRoutingData               `json:"cf,omitempty"`                //Control floor agent will echo anything under the 'cf' tag
    Tag         json.RawMessage `json:"tag,omitempty"`     //If non-blank, will be echoed back on reply
    MessageType string          `json:"type,omitempty"`    //response, notice
    Status      string          `json:"status,omitempty"`  //Currently: ok,error,or notice type (ORIENTATION_CHANGED)
    Error       string          `json:"error,omitempty"` 
    //Renamed Value->RawValue to discourage accidental misuse inside go code
    RawValue    json.RawMessage `json:"value,omitempty"`
}

//Internal requests (to/from server, provider, or agent preserve all Routing data as-is)
//Also, they are presumed to be well-formatted, hence panic() on error
func NewCFResponseFromJSON(b []byte) (*CFResponse, error){
    var r CFResponse;
    err:= json.Unmarshal(b,&r)
    return &r,err
}
func NewCFResponse (r *CFRequest,status string, value interface{},error string)(*CFResponse){
   cfresponse := CFResponse{
       MessageType:"reply",
       Tag:r.Tag,
       Status:status,
       Error:error,
       CFRoutingData:r.CFRoutingData,
   }
   if(value!=nil){
       cfresponse.SetValue(value)
   }
   return &cfresponse
}
func NewOkResponse(r *CFRequest)(*CFResponse){
   return NewCFResponse(r,"ok",nil,"")
}
func NewPongResponse(r *CFRequest)(*CFResponse){
   return NewCFResponse(r,"ok","pong","")
}
func NewErrorResponse(r *CFRequest, error string)(*CFResponse){
   if(r == nil){ //When the server immidiately returns an error without issuing a request
       return &CFResponse{MessageType:"reply",Error:error}
   }
   return NewCFResponse(r,"error",nil,error)
}
func NewOkValueResponse(r *CFRequest, value interface{})(*CFResponse){
   return NewCFResponse(r,"ok",value,"")
}
func (self *CFResponse) JSONBytes() ([]byte,error){
    return json.Marshal(self)
}
func (self *CFResponse) HasValue() bool{
   //TODO: Code below assumes no data returned at all.
   //what if cfagent returns 'null'. Don't do that?
   return len([]byte(self.RawValue)) > 0
}

func (self *CFResponse) ClearRoutingData(){
    self.CFRoutingData = CFRoutingData{}
}
func (self *CFResponse)  GetValue(d interface{}) error{
    return json.Unmarshal(self.RawValue,d)
}
func (self *CFResponse)  SetValue(d interface{}) error{
    if(d == nil){
        self.RawValue = nil //don't want to marshall nil to 'null'. would this matter?
        return nil
    }else{
        var err error
        self.RawValue,err = json.Marshal(d)
        return err
    }
}

type IOSButton struct{
   Description string `json:"description"`
   CenterX float64    `json:"centerX"`
   CenterY float64    `json:"centerY"`
}
type IOSAlert struct{
   Title string        `json:"title"`
   Description string  `json:"description"`
   Buttons []IOSButton `json:"buttons"`
}
type Rectangle struct{
    X float64       `json:"x"`
    Y float64       `json:"y"`
    Width float64   `json:"width"`
    Height float64  `json:"height"`
}


//Customer may set to true or 1,we'd like to be permissive
//Prefer compact "1" in transit. Can delete all of this and force "true" end-to-end if preferred...
type IntBool bool
func (b *IntBool) UnmarshalJSON(data []byte) error {
    asString := string(data)
    if asString == "1" || asString == "true" {
        *b = true
    } else if asString == "0" || asString == "false" {
        *b = false
    } else {
        return fmt.Errorf("Boolean unmarshal error: invalid input %s", asString)
    }
    return nil
}
func (b IntBool) MarshalJSON()([]byte,error) {
    if(b){
        return []byte("1"),nil
    }
    return []byte("0"),nil
}


// NOTE: This function is a shortcut for simple string responses
//       This is distinct from string(self.RawValue), which would return
//       human-readable JSON data, regardless of type
func (self *CFResponse)  StringValue() string{
    var s string
    json.Unmarshal(self.RawValue,&s)
    return s
}

type PathPoint struct {
    X float64  `json:"x"`
    Y float64  `json:"y"`
    Offset float64 `json:"offset"`
}
type Point struct {
    X float64  `json:"x"`
    Y float64  `json:"y"`
}
type Swipe struct {
    Path []PathPoint `json:"path"`
}

func NewSimpleSwipe(x1,y1,x2,y2,offset float64) Swipe{
    var swipe Swipe
    swipe.Path = append(swipe.Path,PathPoint{X:x1,Y:y1,Offset:0})
    swipe.Path = append(swipe.Path,PathPoint{X:x2,Y:y2,Offset:offset})
    return swipe
}

type ElementSearch struct {
    Handle     string  `json:"elementHandle,omitempty"`     //Control Floor cached internal temporary identifier
    ID         string  `json:"elementId,omitempty"`         //iOS element.identifier
    Type       string  `json:"elementType,omitempty"`       //(tap,click)
    SearchPath string  `json:"elementSearchPath,omitempty"` //"application,system","system,application","system",(default)"application"    
    Timeout    float64 `json:"timeout,omitempty"`
    ProcessID  int     `json:"processId,omitempty"`       
}
type Application struct{
    BundleID string  `json:"bundleId"`
}
type Process struct{
    ProcessID int `json:"processId"`
}
type WebRTCRequest struct {
    Offer string `json:"offer"`
}


func NewCFNotice(deviceID string, noticeType string, value interface{}) (*CFResponse){
   cfresponse := CFResponse{
       MessageType:"notice",
       Status:noticeType,
   }
   cfresponse.CFDeviceID = deviceID
   cfresponse.SetValue(value)
   return &cfresponse
}

func NewOrientationChangedNotice(deviceID string, orientation string) *CFResponse {
    return NewCFNotice(deviceID, "ORIENTATION_CHANGED", orientation)
}
func NewAlertDismissedNotice(deviceID string, title string, button string) *CFResponse {
    return NewCFNotice(deviceID, "ALERT_DISMISSED", `Dismissed "`+title+`". Selected "`+button+`"`)
}


