package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// 定义接收数据的结构体
type ScoreData struct {
	Score  string `json:"score"`
	Playid string `json:"playid"`
}

// 返回排行榜数据，成员以及分数
type ZSetMember struct {
	Member string `json:"member"`
	Score  int    `json:"score"`
}

var rdb *redis.Client

// 处理分数上传请求
func scoreLoginHandler(w http.ResponseWriter, r *http.Request) {
	// 只接受post请求
	if r.Method != http.MethodPost {
		fmt.Println("请求方法错误")
		return
	}
	// 解析请求体中的JSON数据
	var requestData ScoreData
	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		fmt.Println("解析请求体错误")
		return
	}
	defer r.Body.Close()
	// 获取玩家id和分数
	score := requestData.Score
	playid := requestData.Playid

	// 将字符串类型的分数转换为float64类型
	fscore, err := strconv.ParseFloat(score, 64)
	if err != nil {
		fmt.Println("转化分数错误")
		return
	}

	// 将分数和玩家ID存入Redis的排序set中，表名为"scores"，字段名为"playid"的值，值为"score"
	timestamp := float64(time.Now().UnixNano()) / 1e9 // 获取当前时间戳，单位为秒
	combinedScore := fscore + timestamp/1e10          // 分数+时间戳，保证分数相同，先来的在前面
	//fmt.Println("分数+时间为: ", combinedScore, "玩家id为:", playid)

	ctx := context.Background() // 创建context.Background()上下文
	z := &redis.Z{
		Score:  combinedScore,
		Member: playid,
	}
	err = rdb.ZAdd(ctx, "scores", z).Err()
	if err != nil {
		fmt.Println("redis添加错误", err)
	}

	// 使用ZRevRangeWithScores从排序集合中获取分数最高的前10个元素及分数（根据需要也可以调整多个）这个函数分数相同保持成员在集合中的原始添加顺序，即先来靠前
	valWithScores, err := rdb.ZRevRangeWithScores(context.Background(), "scores", 0, int64(9)).Result()
	if err != nil {
		fmt.Println("redis获取数据失败", err)
	}

	// 将valWithScores转换为自定义的结构体切片
	var zSetMembers []ZSetMember
	for _, item := range valWithScores {
		// 取分数+时间，将小数点后面的扔掉，小数点后面的是时间
		zSetMembers = append(zSetMembers, ZSetMember{Member: item.Member.(string), Score: int(item.Score)})
	}

	// 将结构体切片转换为JSON格式
	jsonData, err := json.Marshal(zSetMembers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 设置响应头为JSON类型，并写入JSON数据到响应中
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonData)
}

func main() {
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Redis服务器地址
		Password: "",               // 密码
		DB:       2,                // 0用来练习了，1是存palyid，open ID ，unionid的，2，用来存zset
	})
	http.HandleFunc("/scoreload", scoreLoginHandler)
	fmt.Println("服务器启动成功,监听8081端口")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
