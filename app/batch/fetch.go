package main

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/bryutus/caspian-serverside/app/conf"
	"github.com/bryutus/caspian-serverside/app/db"
	"github.com/bryutus/caspian-serverside/app/models"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
)

const datetimeFormat = "2006-01-02 15:04:05"

var apiConfigs map[string]string
var logfile os.File

type Resource struct {
	ArtistName string `json:"artistName"`    // artist name
	ArtistURL  string `json:"artistUrl"`     // artist page URL
	ArtworkURL string `json:"artworkUrl100"` // jacket picture URL
	Copyright  string `json:"copyright"`     // copyright
	Name       string `json:"name"`          // album/song name
	URL        string `json:"url"`           // album/song URL
}

type Feed struct {
	Outline struct {
		Updated   string     `json:"updated"`
		APIURL    string     `json:"id"`
		Resources []Resource `json:"results"`
	} `json:"feed"`
}

type FeedMap map[string]Feed
type HistoryMap map[string]models.History

func init() {
	logfile, err := os.OpenFile(conf.GetLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic("Failed to open log file: " + err.Error())
	}

	log.SetOutput(io.Writer(logfile))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func main() {
	defer logfile.Close()

	apiConfigs = conf.GetAppleApis()

	eg := errgroup.Group{}

	feeds := make(FeedMap)
	for resource, apiURL := range apiConfigs {
		resource := resource
		apiURL := apiURL
		eg.Go(func() error {
			return fetchResources(&feeds, resource, apiURL)
		})
	}

	if err := eg.Wait(); err != nil {
		log.Printf("[ERROR] %s", err.Error())
		return
	}

	db, err := db.Connect()
	if err != nil {
		log.Printf("[ERROR] %s", err.Error())
		return
	}
	defer db.Close()

	histories := make(HistoryMap)
	if err := getHistories(&histories, db); err != nil {
		if err.Error() != "record not found" {
			log.Printf("[ERROR] %s", err.Error())
			return
		}
	}

	for resource := range apiConfigs {
		f := feeds[resource]
		h := histories[resource]

		apiUpdated := parseDatetime(f.Outline.Updated)
		historyUpdated := parseDatetime(h.ApiUpdatedAt)

		// APIから取得したupdatedが前回取得時と同じであれば、
		// APIの更新がないと判断して登録は行わない
		if apiUpdated == historyUpdated {
			continue
		}

		tx := db.Begin()

		history, err := createHistory(apiUpdated, resource, f.Outline.APIURL, tx)
		if err != nil {
			log.Printf("[ERROR] %s", err.Error())
			tx.Rollback()
			continue
		}

		for _, resource := range f.Outline.Resources {
			if err := createResource(history.Model.ID, &resource, tx); err != nil {
				log.Printf("[ERROR] %s", err.Error())
				tx.Rollback()
				continue
			}
		}

		tx.Commit()
	}

	return
}

func fetchResources(feeds *FeedMap, resource string, apiURL string) error {
	res, err := http.Get(apiURL)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var feed Feed
	if err := json.Unmarshal(body, &feed); err != nil {
		return err
	}
	(*feeds)[resource] = feed

	return nil
}

func parseDatetime(datetime string) string {
	if datetime == "" {
		return datetimeFormat
	}

	timestamp, _ := time.Parse(time.RFC3339, datetime)

	return timestamp.Format(datetimeFormat)
}

func getHistories(histories *HistoryMap, db *gorm.DB) error {
	for resource := range apiConfigs {

		h := models.History{}
		if err := db.Where("resource_type = ?", resource).Last(&h).Error; err != nil {
			return err
		}

		(*histories)[resource] = h
	}

	return nil
}

func createHistory(apiUpdated string, resource, apiURL string, db *gorm.DB) (models.History, error) {
	h := models.History{
		ApiUpdatedAt: apiUpdated,
		ResourceType: resource,
		ApiUrl:       apiURL,
	}

	err := db.Create(&h).Error

	return h, err
}

func createResource(id uint, resource *Resource, db *gorm.DB) error {
	r := models.Resource{
		HistoryId:  id,
		Name:       resource.Name,
		Url:        resource.URL,
		ArtworkUrl: resource.ArtworkURL,
		ArtistName: resource.ArtistName,
		ArtistUrl:  resource.ArtistURL,
		Copyright:  resource.Copyright,
	}

	if err := db.Create(&r).Error; err != nil {
		return err
	}

	return nil
}
