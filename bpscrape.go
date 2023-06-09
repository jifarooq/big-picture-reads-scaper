package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
)

var local bool

type article struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func main() {
	if os.Getenv("LAMBDA_TASK_ROOT") != "" {
		lambda.Start(handleRequest)
	} else {
		local = true
		handleRequest()
	}
}

func handleRequest() (string, error) {
	// Find either today's post within root document, or use the provided post
	postURL := os.Getenv("POST_URL")
	if postURL == "" {
		// Load Big Picture blog root document
		blogDoc, err := loadDocument("https://ritholtz.com/category/links/")
		if err != nil {
			fmt.Println("Err loading ritholtz.com:", err)
			return "", err
		}

		blogDoc.Find(".post-title-link").EachWithBreak(func(i int, s *goquery.Selection) bool {
			title, _ := s.Attr("title")
			title = strings.ToLower(title)

			if (strings.Contains(title, "day") || strings.Contains(title, "weekend")) && strings.Contains(title, "read") {
				postURL, _ = s.Attr("href")
				return false
			}
			return true
		})
	}

	// Load post document
	postDoc, err := loadDocument(postURL)
	if err != nil {
		fmt.Println("Err loading blog post", err)
		return "", err
	}

	// Scrape articles from post
	var articles []article
	postDoc.Find("div[itemprop='articleBody'] blockquote p a").Each(func(i int, s *goquery.Selection) {
		url, _ := s.Attr("href")
		articles = append(articles, article{URL: url})
	})

	// Add page titles
	addPageTitles(articles)

	// Build json & send or print
	data, _ := json.Marshal(articles)
	if local {
		fmt.Println(string(data))
	} else {
		err = sendEmail(data, postURL)
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
		fmt.Println("Processing article url", url)

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

func sendEmail(data []byte, postURL string) error {
	var (
		toFrom  = os.Getenv("EMAIL_ADDRESS")
		subject = "big picture reads json " + postURL
		body    = string(data)
		charSet = "UTF-8"
	)

	// Create a new aws session.
	sess, err := session.NewSession(&aws.Config{Region: aws.String("us-west-2")})
	if err != nil {
		return err
	}

	// Create an SES session.
	svc := ses.New(sess)

	// Content helper
	var buildContent = func(text string) *ses.Content {
		return &ses.Content{
			Charset: aws.String(charSet),
			Data:    aws.String(text),
		}
	}

	// Assemble the email.
	input := &ses.SendEmailInput{
		Destination: &ses.Destination{
			ToAddresses: []*string{aws.String(toFrom)},
		},
		Message: &ses.Message{
			Body: &ses.Body{
				// Html: ...
				Text: buildContent(body),
			},
			Subject: buildContent(subject),
		},
		Source: aws.String(toFrom),
	}

	// Attempt to send the email.
	result, err := svc.SendEmail(input)

	// Display error messages if they occur.
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ses.ErrCodeMessageRejected:
				fmt.Println(ses.ErrCodeMessageRejected, aerr.Error())
			case ses.ErrCodeMailFromDomainNotVerifiedException:
				fmt.Println(ses.ErrCodeMailFromDomainNotVerifiedException, aerr.Error())
			case ses.ErrCodeConfigurationSetDoesNotExistException:
				fmt.Println(ses.ErrCodeConfigurationSetDoesNotExistException, aerr.Error())
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and Message from an error.
			fmt.Println(err.Error())
		}

		return err
	}

	fmt.Println("Email Sent to address: " + toFrom)
	fmt.Println(result)
	return nil
}
