package service

import (
	"fmt"
	"strings"
	"os"
	"strconv"
	"net/http"
	"io/ioutil"
	"encoding/json"
	"io"
	"regexp"
	"path/filepath"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/fennecs/zhihuimage/entity"
	"github.com/pkg/errors"
)

const Url = "https://www.zhihu.com/api/v4/questions/{QUESTION_ID}/answers?include=content&limit={LIMIT}&offset={OFFSET}&sort_by=default"
const ZhihuUrl = "https://www.zhihu.com/question/{QUESTION_ID}"
const SizeMax = 5

func GetWonderfulImages(questionId int64, rootDir string, size int, answerLimit int) {
	totalCount := getAnswerCount(questionId)
	if totalCount == 0 {
		fmt.Println("Exit.")
		os.Exit(0)
	}

	rootDir = dirCheck(rootDir, questionId)
	size = sizeCheck(size)

	for i := 0; i < totalCount; i += size {
		// 分页参数
		offset := i
		limit := size
		// 获取api
		api := getApi(questionId, limit, offset)
		fmt.Println("Api:", api)
		// 拿这个api去下载咯
		downloadImageByApi(api, rootDir)

		fmt.Println("----------------------------------------------------------------------")

		if offset/size+1 >= answerLimit {
			fmt.Println("Hit page limit.System exit.")
			os.Exit(0)
		}
	}
}

// 每页最多几条回答
func sizeCheck(size int) int {
	if size > SizeMax || size <= 0 {
		return SizeMax
	}
	return size
}

// 检验文件夹路径 & 创建目录
func dirCheck(path string, questionId int64) string {
	if !filepath.IsAbs(path) {
		path, _ = filepath.Abs(path)
	}

	path = filepath.Clean(path) + "/" + strconv.FormatInt(questionId, 10)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("path not exists, creating ", path)
			err := os.MkdirAll(path, os.ModePerm)

			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	}

	return path + "/"
}

func getApi(questionId int64, limit int, offset int) string {
	api := Url
	api = strings.Replace(api, "{QUESTION_ID}", strconv.FormatInt(questionId, 10), 1)
	api = strings.Replace(api, "{LIMIT}", strconv.Itoa(limit), 1)
	api = strings.Replace(api, "{OFFSET}", strconv.Itoa(offset), 1)
	return api
}

func getAnswerCount(questionId int64) int {
	response, err := http.Get(strings.Replace(ZhihuUrl, "{QUESTION_ID}", strconv.FormatInt(questionId, 10), 1))
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	doc, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		panic(err)
	}
	answerCountContent, exist := doc.Find(".App-main").Find("meta[itemProp=answerCount]").Eq(0).Attr("content")
	if !exist {
		fmt.Println("未能找到总回答数，可能页面不存在")
		return 0
	}

	answerCount, err := strconv.Atoi(answerCountContent)
	if err != nil {
		panic(err)
	}

	fmt.Println("the question has total:", answerCount, "answers")
	return answerCount
}

func downloadImageByApi(url string, rootDir string) {
	results := make([]*entity.Image, 0)
	answers, err := getPagingAnswer(url)
	if err != nil {
		return
	}

	for _, answer := range answers.Data {

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(answer.Content))
		if err != nil {
			fmt.Errorf("failed when read from answer.content. %s", err)
			continue
		}

		doc.Find("figure img").Each(func(i int, selection *goquery.Selection) {
			originImgUrl, exist := selection.Attr("data-original")
			if !exist {
				fmt.Println("不存在原图,可能是个表情包,跳过下载")
				return
			}

			result := make(chan bool)
			image := &entity.Image{
				Path:   originImgUrl,
				Result: result,
			}

			go doDownload(image, rootDir)
			results = append(results, image)
		})
	}

	fmt.Println("All task started, wating for finish...")

	waitForFinish(results)
}

func getPagingAnswer(url string) (*entity.PagingAnswer, error) {
	body, err := getApiBodyWithRetries(url, 0, 5)
	if err != nil {
		return nil, err
	}

	//fmt.Println(string(body))
	answers := new(entity.PagingAnswer)
	err = json.Unmarshal(body, answers)
	if err != nil {
		return nil, errors.New("parse json failed")
	}
	return answers, nil
}

func getApiBodyWithRetries(url string, retries int, maxRetries int) ([]byte, error) {
	var body []byte

	response, err := http.Get(url)
	if err != nil {
		// 递归还是不用defer吧
		response.Body.Close()

		// 超出重试次数
		if retries++; retries > maxRetries {
			fmt.Println(url, err, "[达到最大重试次数，放弃]")
			return nil, errors.New("重试失败")
		} else {
			fmt.Println(url, err, "[挂起10s，准备第", retries, "次重试]")
			time.Sleep(time.Duration(10) * time.Second)
			// 新一轮的尝试
			return getApiBodyWithRetries(url, retries, maxRetries)
		}
	} else {
		body, err = ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Println(err)
			return nil, errors.New("read body failed")
		}
		response.Body.Close()
	}
	return body, nil
}

func waitForFinish(results []*entity.Image) {
	for _, image := range results {
		fmt.Println(image.Path, <-image.Result)
		close(image.Result)
	}
}

func doDownload(image *entity.Image, imgDir string) {
	res, err := http.Get(image.Path)
	if err != nil {
		fmt.Errorf("failed when downloading image. %s", err)
		image.Result <- false
		return
	}
	defer res.Body.Close()

	// 创建文件
	name := getFileName(image.Path)
	out, err := os.Create(imgDir + name)
	if err != nil {
		fmt.Errorf("failed when creating file. %s", err)
		image.Result <- false
		return
	}

	// 下载
	_, err = io.Copy(out, res.Body)
	if err != nil {
		fmt.Println(err)
		image.Result <- false
		return
	}

	image.Result <- true
}

func getFileName(path string) string {
	reg, _ := regexp.Compile(`(\w|\d|_)*.jpg`)
	name := reg.FindStringSubmatch(path)[0]
	return name
}
