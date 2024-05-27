package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	appID     = "wx8cc47e6c7f4d0a7b"
	appSecret = "138b5f23cf47f675d31fe62e317126f0"
	wxAPIURL  = "https://api.weixin.qq.com/sns/jscode2session"
)

// 存放微信API的响应
type WXResponse struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// 定义接收数据的结构体
type RequestData struct {
	Code          string `json:"code"`
	EncryptedData string `json:"encryptedData"`
	IV            string `json:"iv"`
}

var rdb *redis.Client //全局变量
var collection *mongo.Collection
var ctx context.Context

// 解密微信数据
func DecryptWechatData(encryptedData, sessionKey, iv string) (string, error) {
	// base64解码
	cipherText, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", fmt.Errorf("base64 decode error: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(sessionKey)
	if err != nil {
		return "", fmt.Errorf("base64 decode session_key error: %v", err)
	}
	ivBytes, err := base64.StdEncoding.DecodeString(iv)
	if err != nil {
		return "", fmt.Errorf("base64 decode iv error: %v", err)
	}

	// AES CBC模式解密
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("new cipher error: %v", err)
	}
	if len(cipherText)%block.BlockSize() != 0 {
		return "", fmt.Errorf("cipherText is not a multiple of the block size")
	}
	mode := cipher.NewCBCDecrypter(block, ivBytes)
	mode.CryptBlocks(cipherText, cipherText)

	// 去除填充
	cipherText = PKCS7Unpad(cipherText)
	return string(cipherText), nil

}

// PKCS7Unpad
func PKCS7Unpad(data []byte) []byte {
	length := len(data)
	unpadding := int(data[length-1])
	return data[:(length - unpadding)]
}

// 对具有唯一性的openid进行加密生成playid
func EncryptOpenID(openid string) string {
	encrypted := []rune(openid)
	for i, char := range encrypted {
		encrypted[i] = (char-32+1)%95 + 32 // 保证每个字符都是可见字符
	}
	return string(encrypted)
}

// 每隔一秒检查并处理过期的redis数据
func checkAndPersistExpiredRedisData(ctx context.Context, rdb *redis.Client, collection *mongo.Collection) {
	ticker := time.NewTicker(1 * time.Second) // 每1秒检查一次
	//fmt.Println("开始检查并处理过期的redis数据")
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// 如果主goroutine的context被取消，则停止此goroutine
			return
		case <-ticker.C:
			// 定时器触发，检查并处理过期的Redis数据
			handleExpiredRedisData(ctx, rdb, collection)
		}
	}
}

// 检查并处理过期的Redis数据
func handleExpiredRedisData(ctx context.Context, rdb *redis.Client, collection *mongo.Collection) {
	// 使用SCAN命令遍历Redis中的所有键
	iter := rdb.Scan(ctx, 0, "", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		// 跳过空键
		if key == "" {
			continue
		}
		//fmt.Println("此时key是", key)
		// 获取键的TTL
		ttl, err := rdb.TTL(ctx, key).Result()
		if err != nil {
			log.Printf("Error getting TTL for key %s: %v", key, err)
			continue
		}
		ttlsecond := int(ttl.Seconds())
		//fmt.Println("此时ttl是", ttlsecond)
		// 检查键是否过期（TTL <= 5） emm这里redis过期可能会删除键，这样就得不到键从而得不到值，所以提前5秒进行持久化
		if ttlsecond <= 5 {
			// 从Redis中获取值
			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				log.Printf("Error getting value for key %s: %v", key, err)
				continue
			}
			fmt.Println("此时openid是", val)
			// 手动指定mongoDB的插入文档的格式并插入
			var data bson.M = bson.M{
				"_id":    key, // 这里手动指定_id
				"openid": val,
			}
			_, err = collection.InsertOne(ctx, data)
			if err != nil {
				log.Printf("Error inserting data into MongoDB for key %s: %v", key, err)
				continue
			}
			//fmt.Println("持久化成功")
			// 十分钟的话，存活时间设置长一些+5秒 , 删除Redis中的键，到时间删除
			rdb.Del(ctx, key)
		}
	}

	if err := iter.Err(); err != nil {
		log.Println("遍历redis中键值失败", err)
	}
}

// TransferDataToRedis 从MongoDB加载数据并将其存储到Redis
func TransferDataToRedis(mongoURI, mongoDB, mongoCollection, redisAddr string) {
	// 连接到MongoDB
	mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Println("failed to connect to MongoDB: %w", err)
	}
	defer mongoClient.Disconnect(context.Background())

	mongoColl := mongoClient.Database(mongoDB).Collection(mongoCollection)

	// 连接到Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       1,
	})
	// 从MongoDB查询数据并存储到Redis
	cursor, err := mongoColl.Find(context.Background(), bson.M{})
	if err != nil {
		fmt.Println("failed to query MongoDB: %w", err)
	}
	defer cursor.Close(context.Background())
	// 遍历MongoDB游标，将数据存储到Redis
	for cursor.Next(context.Background()) {
		var result bson.M
		if err := cursor.Decode(&result); err != nil {
			fmt.Println("failed to decode MongoDB document: %w", err)
		}
		// "{\"_id\":\"fkol2_A6fNj9C1OfEKrJVZlDCRseyU\",\"openid\":\"ol2_A6fNj9C1OfEKrJVZlDCRseyU\"}" 切片操作处理格式
		id, ok := result["_id"].(string) // 获取MongoDB文档中的_id字段
		if !ok {
			fmt.Println("missing or invalid _id field in MongoDB document")
		}
		//fmt.Println("此时id是:", id)
		data, err := json.Marshal(result) // 将MongoDB文档转换为JSON
		if err != nil {
			fmt.Println("failed to marshal MongoDB document to JSON: %w", err)
		} else {
			pre := `"openid":"`
			ddata := string(data)
			// 查找前缀的位置
			index := strings.Index(ddata, pre)
			// 跳过前缀，找到openid值的起始位置
			index += len(pre)
			// 从起始位置查找openid值的结束位置（即下一个双引号的位置）
			endIndex := strings.IndexByte(ddata[index:], '"')
			// endIndex是相对于index的偏移量，因此需要加上index来得到在原始字符串中的正确位置
			endIndex += index
			// 提取openid值存入redis中
			keyopenid := ddata[index:endIndex]
			if err := redisClient.Set(context.Background(), id, keyopenid, time.Hour).Err(); err != nil {
				fmt.Println("failed to set key in Redis: %w", err)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		fmt.Println("MongoDB cursor error: %w", err)
	}
}

// 处理微信登录请求 & 生成返回用户唯一ID
func wxLoginHandler(w http.ResponseWriter, r *http.Request) {
	// 只接受post请求
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request method.", http.StatusMethodNotAllowed)
		return
	}
	// 解析请求体中的JSON数据
	var requestData RequestData
	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 输出接收到的数据
	code := requestData.Code
	if code == "" {
		http.Error(w, "Code is required", http.StatusBadRequest)
		return
	}
	encryptedData := requestData.EncryptedData
	iv := requestData.IV
	//fmt.Println("encryptedData是:", encryptedData) //查看encryptedData是否出错
	//fmt.Println("iv是:", iv)                       //查看iv是否出错

	// 构建微信的请求URL
	url := fmt.Sprintf("%s?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code", wxAPIURL, appID, appSecret, code)
	//fmt.Println("url是", url) //查看url是否出错
	resp, err := http.Get(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 解析响应体的JSON数据
	var wxResp WXResponse
	if err := json.Unmarshal(body, &wxResp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wxResp.ErrCode != 0 {
		http.Error(w, wxResp.ErrMsg, http.StatusInternalServerError)
		return
	}

	// 查看openid以及session_key是否正确
	openid := wxResp.OpenID
	sessionkey := wxResp.SessionKey
	//unionid := wxResp.UnionID
	//fmt.Println("openid是", openid)
	//fmt.Println("sessiongkey是", sessionkey)
	//fmt.Println("unionid是", unionid)      // 需要绑定微信开发平台，认证需要300元...

	// 加密openid作为playid（PlayID），这里使用过AES加密但生成不具有唯一性，最好的方法应该是分布式下维护一个全局变量比较好，每个玩家登陆id就++。
	playid := EncryptOpenID(openid)

	// 解密用户信息数据数据 (绑定完微信开发平台之后也能通过这个里面内容获取UnionID)
	decrypted, err := DecryptWechatData(encryptedData, sessionkey, iv)
	if err != nil {
		fmt.Println("解密数据失败:", err)
		return
	}
	fmt.Println("解密后的数据是:", decrypted) // 用户信息数据，包括openid, unionid, nickname, gender, province, city, country, avatarUrl

	// 存入opneid，playid (unionid需要认证绑定获取) 到redis中
	ctx = context.Background()
	err = rdb.Set(ctx, playid, openid, 10*time.Minute+5*time.Minute).Err() // 10分钟过期+5秒提前预处理 ，key是playid，value是openid
	if err != nil {
		log.Fatalf("向redis中存入值失败: %v", err)
	}

	// 启动一个go协程来定时检查并处理过期redis数据，并持久化到mongdb中去
	go checkAndPersistExpiredRedisData(context.Background(), rdb, collection)

	// 给客户端发挥响应
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(playid))
}

func main() {
	// 初始化redis连接
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Redis服务器地址
		Password: "",               // 密码
		DB:       1,                // 0用来练习了，1正式环境
	})
	// 初始化mongo连接
	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	err = client.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal(err)
	}
	collection = client.Database("ypw").Collection("ypw")
	fmt.Println("Connected to MongoDB!")

	// 启动一个go程每次登陆将数据从mongdb中同步到redis中
	go TransferDataToRedis("mongodb://localhost:27017", "ypw", "ypw", "localhost:6379")

	http.HandleFunc("/wxlogin", wxLoginHandler)
	fmt.Println("服务器启动成功,监听8080端口")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
