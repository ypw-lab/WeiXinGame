require("./weapp-adapter");

// 获取canvas对象
const canvas = window.canvas;
// 获取2d渲染上下文
const ctx = canvas.getContext("2d");
var playid = ""
// 获取画布的宽度和高度
const width = canvas.width;
const height = canvas.height;
// 设置按钮的位置和大小
const buttonX = 0;  // 按钮的 x 坐标 85
const buttonY = 0;  // 按钮的 y 坐标 450
const buttonWidth = 100;  // 按钮的宽度
const buttonHeight = 40;  // 按钮的高度
//背景图绘制
const image = wx.createImage();
image.onload = function (){
    ctx.drawImage(image, 0, 0, width, height);
    // 建立登陆按钮
    const buttonImage = wx.createImage();
    buttonImage.src = './image/log.png';  
    buttonImage.onload =() =>{
        ctx.drawImage(buttonImage,buttonX,buttonY,buttonWidth, buttonHeight); 
    };
    // 建立退出按钮
    const buttonImageExit = wx.createImage();
    buttonImageExit.src = './image/exit.png';  
    buttonImageExit.onload =() =>{
        ctx.drawImage(buttonImageExit,buttonX+220,buttonY,buttonWidth, buttonHeight); 
    };

}
image.src = "./image/background.png";
  //检查是否触摸了按钮
wx.onTouchStart((touchEvent) => {
    const touch = touchEvent.touches[0];
    const x = touch.clientX
    const y = touch.clientY
    //console.log(touch.clientX)
    //console.log(touch.clientY)
  // 用户点击了按钮
  if (x >= buttonX && x <= buttonX + buttonWidth &&
      y >= buttonY && y <= buttonY + buttonHeight) {
      //console.log("点击登陆按钮！")
      //音效
      var audio = wx.createInnerAudioContext();
      audio.src = "./audio/dianji.mp3";
      audio.play();
      //获取微信用户信息
      wx.getUserProfile({
        desc: '用于注册登陆游戏', // 声明获取用户个人信息后的用途
        success: (res) => {
          //console.log('获取用户信息成功', res.userInfo) // 通过userInfo可获取头像，昵称，性别等信息
          // 进行登录
          login();
          load();
        },
        fail: () => {
          console.error('用户拒绝授权');
          // 处理用户拒绝授权的情况
        }
      });
  }else{
    //console.log("没点击登陆按钮！")
  }
});

// 定义上传函数
function load(){
        wx.showModal({
          title: '成绩录入',
          content: '请在下面输入框输入你分数(0~999)',
          // success (res) {
          //   if (res.confirm) {
          //     console.log('用户点击确定')
          //   } else if (res.cancel) {
          //     console.log('用户点击取消')
          //   }
          // }
        });
        wx.showKeyboard({
              confirmHold: false,
              confirmType: 'done',
              defaultValue: '',  //这里没有做相应类型检查，先把整体框架搭建完毕
              maxLength: 3,
              multiple: false,
            })
            // 监听传入的值是什么 (这里本来打算做一个简单的判断的，这里这个api是一个异步函数，回调函数只会在用户输入后执行，用while会死循环)
        wx.offKeyboardConfirm(); // 注册事件之前先移除之前的监听器，不然存在多个会请求多次
        wx.onKeyboardConfirm(function(res){
                //console.log('输入的是：',res.value); 
               // console.log('玩家id是：',playid);
                // 简单的上传功能，这个值传给服务器，然后redis进行排行榜功能设计
                sendscoreToServer(res.value,playid) // 排行榜功能实现
        })
}

//发送score到服务器，开另一个进程服务处理这个任务
function sendscoreToServer(score,playid) {
  wx.request({
    url: 'http://localhost:8081/scoreload', // 服务器API地址
    method: 'POST',
    data: { // 不放url，放这里安全
      score: score,
      playid: playid,
    },
    headers: { 'Content-Type': 'application/json' },
    success(res) {
      console.log('服务器返回:', res.data);
    },
    fail(error) {
      console.error('请求失败:', error);
    }
  });
}


// 定义登录函数
function login() {
  wx.login({
      success: function(res) {
          if (res.code) {
              // 将 res.code 发送到后台，换取 openId, sessionKey, unionId ，客户端如果需要可返回
              console.log("登录成功，code:", res.code);
              // 获取encryptedData和iv
              wx.getUserInfo({
                withCredentials: true, 
                success: function(infoResult) {
                 // console.log("加密数据是：",infoResult.encryptedData);
                 // console.log(infoResult.iv);
                  sendCodeToServer(res.code,infoResult.encryptedData,infoResult.iv); // 发送code，encryptedData ，iv信息给服务器
                },
              });
          } else {
              console.log("登录失败：" + res.errMsg);
          }
      }
  });
}

//发送res.code到服务器
function sendCodeToServer(code,encryptedData,iv) {
  wx.request({
    url: 'http://localhost:8080/wxlogin', // 服务器API地址
    method: 'POST',
    data: { // 不放url，放这里安全
      code: code,
      encryptedData: encryptedData,
      iv: iv
    },
    headers: { 'Content-Type': 'application/json' },
    success(res) {
      console.log('服务器返回:', res.data);
      playid = res.data;
    },
    fail(error) {
      console.error('请求失败:', error);
    }
  });
}
