package main

import (
	"encoding/json"
)

//MARK:-  LT Changes ==========Start==========

type CFR_Refresh struct {
	Id      int    `json:"id"`
	Refresh string `json:"refresh"`
}

func (self *CFR_Refresh) asText() string {
	text, _ := json.Marshal(self)
	return string(text)
}

type CFR_Restart struct {
	Id      int    `json:"id"`
	Restart string `json:"restart"`
}

func (self *CFR_Restart) asText() string {
	text, _ := json.Marshal(self)
	return string(text)
}

type CFR_Rotate struct {
	Id     int    `json:"id"`
	Rotate string `json:"rotate"`
}

func (self *CFR_Rotate) asText() string {
	text, _ := json.Marshal(self)
	return string(text)
}

type CFR_LaunchSafariUrl struct {
	Action string `json:"action"`
	Url    string `json:"url"`
}

type CFR_CleanBrowserData struct {
	Action string `json:"action"`
	Bid    string `json:"bid"`
}

type CFR_RotateDevice struct {
	Action      string `json:"action"`
	Orientation string `json:"orientation"`
}

func (self *Device) RotateDevice(orientation string) {
	self.cfa.RotateDevice(orientation)
}

func (self *Device) Refresh() string {
	return self.cfa.Refresh()
}
func (self *Device) Restart() string {
	return self.cfa.Restart()
}

func (self *Device) LaunchSafariUrl(url string) {
	self.cfa.LaunchSafariUrl(url)
}

func (self *Device) CleanBrowserData(bid string) {
	self.cfa.CleanBrowserData(bid)
}

func (self *Device) RestartStreaming() {
	self.cfa.RestartStreaming()
}

//LT Changes ==========End==========
