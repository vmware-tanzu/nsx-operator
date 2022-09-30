package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

type File struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Sign     string `json:"sign"`
	Thumb    string `json:"thumb"`
	Type     int64  `json:"type"`
}

type RawFile struct {
	File
	RawUrl   string `json:"raw_url"`
	Readme   string `json:"readme"`
	Provider string `json:"provider"`
	Related  string `json:"related"`
}

type GeneralRequest struct {
	Path string `json:"path"`
}

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type GeneralResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

type FileResponse struct {
	Code    int     `json:"code"`
	Message string  `json:"message"`
	FileRaw RawFile `json:"data"`
}

type ContentResponse struct {
	Content []File `json:"content"`
}

type FileListResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    ContentResponse `json:"data"`
}

// 用于获取登录token，有效期一般6个小时, 将token放入Header的Authorization。
func TestAuth(t *testing.T) {
	url := "http://localhost:5244/api/auth/login"
	reqBody, err := json.Marshal(AuthRequest{Username: "admin", Password: "6J7RQgNk"})
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var respData GeneralResponse
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		panic(err)
	}
	fmt.Println(respData.Data.(map[string]interface{})["token"])
}

// 用于获取文件列表，每个文件的信息基本上是全的，但是官方库有个bug，下面单个接口获取的缩略图是空的，这里是有的，我修复了下，下面的接口能用了，于是这个接口我们可以不需要了。
func TestList(t *testing.T) {
	url := "http://localhost:5244/api/fs/list"
	reqBody, err := json.Marshal(GeneralRequest{Path: "/a/20230321/"})
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6ImFkbWluIiwiZXhwIjoxNjc5NjY3MTUzLCJuYmYiOjE2Nzk0OTQzNTMsImlhdCI6MTY3OTQ5NDM1M30.Zobvc2NrrNRGd_R10kBCZ9NCVaoFR31sPyLyE48PizE")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var respData FileListResponse
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		panic(err)
	}
	fmt.Println(respData.Data.Content)
}

// 用于获取单个文件的cdn link 和 img link
// 我们基本上只需要把上面的Auth和这个接口加进来即可。
func TestLink(t *testing.T) {
	url := "http://localhost:5244/api/fs/get"
	reqBody, err := json.Marshal(GeneralRequest{Path: "/a/202303232204/ZHWnezmZS4dMutHO.mkv"})
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6ImFkbWluIiwiZXhwIjoxNjc5NzUxMDQ3LCJuYmYiOjE2Nzk1NzgyNDcsImlhdCI6MTY3OTU3ODI0N30.u7gwPJHCuhR67PG0731PEPNvmbOqUiPc_Y5kZw4mpxo")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var respData FileResponse
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		panic(err)
	}
	fmt.Println(respData.FileRaw.RawUrl)
	fmt.Println(respData.FileRaw.Thumb)
	
}
