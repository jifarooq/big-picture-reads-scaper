package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/aws/aws-lambda-go/lambda"
)

type article struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func main() {
	lambda.Start(handleRequest)
}

func handleRequest() (string, error) {
	// Load Big Picture blog root document
	blogDoc, err := loadDocument("http://ritholtz.com/")
	if err != nil {
		log.Fatal(err)
		return "", err
	}

	// Find today's post within root document
	var postURL string
	blogDoc.Find(".post-title-link").EachWithBreak(func(i int, s *goquery.Selection) bool {
		title, _ := s.Attr("title")
		title = strings.ToLower(title)

		if strings.Contains(title, "day") && strings.Contains(title, "read") {
			postURL, _ = s.Attr("href")
			return false
		}
		return true
	})

	// Load post document
	postDoc, err := loadDocument(postURL)
	if err != nil {
		log.Fatal(err)
		return "", err
	}

	// Scrape articles from post
	var articles []article
	postDoc.Find("blockquote p a").Each(func(i int, s *goquery.Selection) {
		url, _ := s.Attr("href")
		articles = append(articles, article{URL: url})
	})

	// Add page titles
	addPageTitles(articles)

	// Build json & send
	data, _ := json.Marshal(articles)
	err = sendSimpleMessage(data)
	if err != nil {
		fmt.Println(err)
	}
	return "", err
}

func loadDocument(url string) (*goquery.Document, error) {
	// Make request
	res, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, errors.New(fmt.Sprintf("status code error: %d %s", res.StatusCode, res.Status))
	}

	// Load the HTML document
	return goquery.NewDocumentFromReader(res.Body)
}

func addPageTitles(articles []article) {
	for i := range articles {
		url := articles[i].URL

		// Damn it Bloomberg!
		if strings.Contains(url, "bloomberg") {
			articles[i].Title = getBlockedPageTitle(url)
			continue
		}

		// Prep PDF and fallback titles
		split := strings.Split(url, "/")
		lastInPath := split[len(split)-1]

		// PDFs...
		if strings.HasSuffix(url, ".pdf") {
			articles[i].Title = lastInPath
			continue
		}

		// Normal case...
		artDoc, err := loadDocument(url)
		if err != nil {
			articles[i].Title = lastInPath
			continue
		}

		articles[i].Title = artDoc.Find("title").First().Text()
	}
}

func getBlockedPageTitle(url string) string {
	// example -> https://www.bloomberg.com/opinion/articles/2020-05-04/texas-versus-california-a-story-of-dueling-coronavirus-rules/
	url = strings.TrimSuffix(url, "/")
	urlSplit := strings.Split(url, "/")

	title := urlSplit[len(urlSplit)-1]
	title = strings.Title(title)

	return strings.ReplaceAll(title, "-", " ")
}

func sendSimpleMessage(data []byte) error {
	// Env vars
	var (
		sandboxID = os.Getenv("MAILGUN_SANDBOX_ID")
		apiKey    = os.Getenv("MAILGUN_API_KEY")
		emailAddr = os.Getenv("EMAIL_ADDRESS")
	)

	now := int(time.Now().Unix())

	// Build payload
	vals := neturl.Values{}
	vals.Add("from", fmt.Sprintf("mailgun me <postmaster@sandbox%s.mailgun.org>", sandboxID))
	vals.Add("to", fmt.Sprintf("Justin <%s>", emailAddr))
	vals.Add("subject", "big picture reads json "+strconv.Itoa(now))
	vals.Add("text", string(data))
	body := []byte(vals.Encode())

	// Init req
	url := "https://api.mailgun.net/v3/sandbox" + sandboxID + ".mailgun.org/messages"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	// Add auth & build client
	req.SetBasicAuth("api", apiKey)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	cli := &http.Client{}

	// Make request
	res, err := cli.Do(req)
	if res.StatusCode/100 != 2 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(res.Body)
		err = errors.New(buf.String())
	}

	return err
}
