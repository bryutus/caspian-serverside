package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bryutus/caspian-serverside/app/conf"
	"github.com/bryutus/caspian-serverside/app/db"
	"github.com/bryutus/caspian-serverside/app/models"
	_ "github.com/jinzhu/gorm/dialects/mysql"
)

const datetime_format = "2006-01-02 15:04:05"

// Result アルバム/ソングの情報
type Result []struct {
	ArtistName string `json:"artistName"`    // artist name
	ArtistUrl  string `json:"artistUrl"`     // artist page URL
	ArtworkUrl string `json:"artworkUrl100"` // jacket picture URL
	Copyright  string `json:"copyright"`     // copyright
	Name       string `json:"name"`          // album/song name
	Url        string `json:"url"`           // album/song URL
}

// Lanking RSS Feedのアウトライン
type Lanking struct {
	Outline struct {
		Updated string `json:"updated"`
		ApiUrl  string `json:"id"`
		Results Result `json:"results"`
	} `json:"feed"`
}

type Lankings map[string]Lanking
type Histories map[string]models.History

func main() {
	// ロギングの設定
	logfile, err := os.OpenFile(conf.GetLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic("Failed to open log file: " + err.Error())
	}
	defer logfile.Close()

	log.SetOutput(io.Writer(logfile))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	lankings := make(Lankings)

	var waitGroup sync.WaitGroup

	types := conf.GetAppleApis()

	for k, v := range types {
		waitGroup.Add(1)

		go func(resourceType, resource string) {
			defer waitGroup.Done()

			res, err := http.Get(resource)

			if err != nil {
				fmt.Println(err)
				return
			}

			defer res.Body.Close()

			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				fmt.Println(err)
				return
			}

			var lanking Lanking
			if err := json.Unmarshal(body, &lanking); err != nil {
				fmt.Println(err)
				return
			}
			lankings[resourceType] = lanking
		}(k, v)
	}

	waitGroup.Wait()

	db := db.Connect()
	defer db.Close()

	histories := make(Histories)

	for k, _ := range types {
		h := models.History{}
		db.Where("resource_type = ?", k).Last(&h)
		histories[k] = h
	}

	for resourceType, _ := range types {
		l := lankings[resourceType]
		h := histories[resourceType]

		apiUpdated := parseDatetime(l.Outline.Updated)
		updated := parseDatetime(h.ApiUpdatedAt)

		if apiUpdated == updated {
			continue
		}

		history := models.History{
			ApiUpdatedAt: apiUpdated,
			ResourceType: resourceType,
			ApiUrl:       l.Outline.ApiUrl,
		}
		db.Create(&history)

		for _, r := range l.Outline.Results {
			db.Create(&models.Resource{
				HistoryId:  history.Model.ID,
				Name:       r.Name,
				Url:        r.Url,
				ArtworkUrl: r.ArtworkUrl,
				ArtistName: r.ArtistName,
				ArtistUrl:  r.ArtistUrl,
				Copyright:  r.Copyright,
			})
		}
	}
}

func parseDatetime(datetime string) string {
	timestamp, err := time.Parse(time.RFC3339, datetime)

	if err != nil {
		fmt.Println(err)
		return "err"
	}

	return timestamp.Format(datetime_format)
}
