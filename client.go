package skype

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/gogf/gf/encoding/gurl"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Conn struct {
	LoggedIn          bool //has logged in or not
	session           *Session
	sessionLock       uint32
	Store             *Store
	handler           []Handler
	LoginInfo         *Session
	UserProfile       *UserProfile
	ConversationsList *ConversationsList
	*MessageClient
	*ContactClient
	CreateChan chan string
}

type UserProfile struct {
	About       string   `json:"about"`
	AvatarUrl   string   `json:"avatarUrl"`
	Birthday    string   `json:"birthday"`
	City        string   `json:"city"`
	Country     string   `json:"country"`
	Emails      []string `json:"emails"`
	FirstName   string   `json:"firstname"`
	Gender      string   `json:"gender"`
	Homepage    string   `json:"homepage"`
	JobTitle    string   `json:"jobtitle"`
	Language    string   `json:"language"`
	LastName    string   `json:"lastname"`
	Mood        string   `json:"mood"`
	PhoneHome   string   `json:"phone_home"`
	PhoneOffice string   `json:"phone_office"`
	Province    string   `json:"province"`
	RichMood    string   `json:"rich_mood"`
	Username    string   `json:"username"` //live:xxxxxxx
}

func NewConn() (cli *Conn, err error) {
	c := &Conn{
		handler:    make([]Handler, 0),
		LoggedIn: false,
		session:  nil,
		Store:      newStore(),
		ContactClient: &ContactClient{},
		MessageClient: &MessageClient{},
		CreateChan: nil,
	}
	return c, nil
}

func (c *Conn) IsLoginInProgress() bool {
	return c.sessionLock == 1
}

/**
login Skype by web auth
*/
func (c *Conn) Login(username, password string) (err error) {
	//Makes sure that only a single Login or Restore can happen at the same time
	if !atomic.CompareAndSwapUint32(&c.sessionLock, 0, 1) {
		return errors.New("login or restore already running")
	}
	defer atomic.StoreUint32(&c.sessionLock, 0)

	if c.LoggedIn {
		username := c.UserProfile.FirstName
		if len(c.UserProfile.LastName) > 0 {
			username = username + c.UserProfile.LastName
		}
		return errors.New("You are already logged in as @" + username)
	}

	//if strings.Index(username, "@") > -1{
	//	err = c.GetTokeBySOAP(username, password)
	//} else {
		err = c.GetTokeByAuthLive(username, password)
	//}
	return
	if err != nil {
		return err
	}

	//获得用户SkypeRegistrationTokenProvider
	c.LoginInfo.LocationHost = API_MSGSHOST
	err = c.SkypeRegistrationTokenProvider(c.LoginInfo.SkypeToken)
	if err != nil {
		return errors.New("SkypeRegistrationTokenProvider get error")
	}

	//请求获得用户的id （类型  string）
	err = c.GetUserId(c.LoginInfo.SkypeToken)
	if err != nil {
		return errors.New("GetUserId get error")
	}
	return
}

// Because the login policy of skype changes,
// this method of obtaining token does not currently work
func (c *Conn) GetTokeByAuthLive(username, password string) error {
	MSPRequ, MSPOK, PPFT, err := c.getParams()
	if MSPOK == "" || MSPRequ == "" || PPFT == "" || err != nil {
		return errors.New("params get error")
	}

	//1. send username password
	paramsMap := url.Values{}
	paramsMap.Set("wa", "wsignin1.0")
	paramsMap.Set("wp", "MBI_SSL")
	paramsMap.Set("wreply", "https://lw.skype.com/login/oauth/proxy?client_id=578134&site_name=lw.skype.com&redirect_uri=https%3A%2F%2Fweb.skype.com%2F")

	cookies := map[string]string{
		"MSPRequ": MSPRequ,
		"MSPOK":   MSPOK,
		"CkTst":   "G" + API_MSACC + strconv.Itoa(time.Now().Second() * 1000),
	}
	//opid, err, _:= c.sendCred(username, password, MSPRequ, MSPOK, PPFT)
	opid, err := c.sendCred1(paramsMap, username, password, PPFT, cookies)
	//return nil
	//opid, err := c.sendCred(paramsMap, username, password, PPFT, cookies)
	if err != nil {
		return errors.New("sendCred get error")
	}
	cookies["CkTst"] = strconv.Itoa(time.Now().Second() * 1000)
	tValue, err := c.sendOpid(paramsMap, PPFT, opid, cookies, username, password)
	//tValue := ""
	fmt.Println()
	fmt.Println(opid)
	fmt.Println(paramsMap)
	fmt.Println(password)
	fmt.Println(tValue)
	fmt.Println("MSPRequ", MSPRequ)
	fmt.Println("MSPOK", MSPOK)
	fmt.Println("PPFT", PPFT)
	if tValue == "" {
		return errors.New("Logig failed, Can not find 't' value")
	}

	//2. get token and RegistrationExpires
	err = c.getToken(tValue)
	if err != nil {
		return errors.New("Get token error ")
	}
	return nil
}

func (c *Conn) GetTokeBySOAP(username, password string) error {
	// An authentication provider that connects via Microsoft account SOAP authentication.
	template := `
    <Envelope xmlns='http://schemas.xmlsoap.org/soap/envelope/'
       xmlns:wsse='http://schemas.xmlsoap.org/ws/2003/06/secext'
       xmlns:wsp='http://schemas.xmlsoap.org/ws/2002/12/policy'
       xmlns:wsa='http://schemas.xmlsoap.org/ws/2004/03/addressing'
       xmlns:wst='http://schemas.xmlsoap.org/ws/2004/04/trust'
       xmlns:ps='http://schemas.microsoft.com/Passport/SoapServices/PPCRL'>
       <Header>
           <wsse:Security>
               <wsse:UsernameToken Id='user'>
                   <wsse:Username>%s</wsse:Username>
                   <wsse:Password>%s</wsse:Password>
               </wsse:UsernameToken>
           </wsse:Security>
       </Header>
       <Body>
           <ps:RequestMultipleSecurityTokens Id='RSTS'>
               <wst:RequestSecurityToken Id='RST0'>
                   <wst:RequestType>http://schemas.xmlsoap.org/ws/2004/04/security/trust/Issue</wst:RequestType>
                   <wsp:AppliesTo>
                       <wsa:EndpointReference>
                           <wsa:Address>wl.skype.com</wsa:Address>
                       </wsa:EndpointReference>
                   </wsp:AppliesTo>
                   <wsse:PolicyReference URI='MBI_SSL'></wsse:PolicyReference>
               </wst:RequestSecurityToken>
           </ps:RequestMultipleSecurityTokens>
       </Body>
    </Envelope>`
	data := fmt.Sprintf(template, ReplaceSymbol(username), ReplaceSymbol(password))

	req := Request{timeout: 30}
	body, err := req.HttpPostWitHeaderAndCookiesJson(fmt.Sprintf("%s/RST.srf", API_MSACC), nil, data, nil, nil)
	if err != nil {
		fmt.Println("getSecToken err: ", err)
		return errors.New("get token err: couldn't retrieve security token from login response")
	}

	var envelopeResult EnvelopeXML
	err = xml.Unmarshal([]byte(body), &envelopeResult)
	if err != nil {
		return errors.New("get token err: parse EnvelopeXML err")
	}
	if envelopeResult.Body.Collection.Response.ReSeToken.BinarySecurityToken == "" {
		if envelopeResult.Fault.FaultCode == "wsse:FailedAuthentication" {
			return errors.New("Please confirm that your account password is entered correctly ")
		}
		return errors.New("get token err: can not find BinarySecurityToken: \n" + body)
	}

	data2 := map[string]interface{}{
		"partner":     999,
		"access_token": envelopeResult.Body.Collection.Response.ReSeToken.BinarySecurityToken,
		"scopes": "client",
	}
	params, _ := json.Marshal(data2)
	body, err = req.HttpPostWitHeaderAndCookiesJson(API_EDGE, nil, string(params), nil, nil)

	if err != nil {
		fmt.Println("exchangeToken err: ", err)
		return errors.New("get token err: exchangeToken err")
	}

	edgeResp := EdgeResp{}
	json.Unmarshal([]byte(body), &edgeResp)
	if edgeResp.SkypeToken == "" || edgeResp.ExpiresIn == 0 {
		return errors.New(fmt.Sprintf("err status code: %s, status text: %s,", strconv.FormatInt(int64(edgeResp.Status.Code), 10), edgeResp.Status.Text))
	}
	c.LoginInfo = &Session{
		SkypeToken:   edgeResp.SkypeToken,
		SkypeExpires: strconv.FormatInt(int64(edgeResp.ExpiresIn), 10),
	}
	return nil
}

type EnvelopeXML struct {
	XMLName  xml.Name `xml:"Envelope"` // 指定最外层的标签为config
	Header string `xml:"Header"` // 读取smtpServer配置项，并将结果保存到SmtpServer变量中
	Body EnvelopeBody `xml:"Body"` // 读取receivers标签下的内容，以结构方式获取
	Fault EnvelopeFault `xml:"Fault"`
}

type EnvelopeBody struct {
	Collection RequestSecurityTokenResponseCollection `xml:"RequestSecurityTokenResponseCollection"`
}

type EnvelopeFault struct {
	FaultCode string `xml:"faultcode"`
	FaultString string `xml:"faultstring"`
}

type RequestSecurityTokenResponseCollection struct {
	Response RequestSecurityTokenResponse `xml:"RequestSecurityTokenResponse"`
}

type RequestSecurityTokenResponse struct {
	TokenType string `xml:"TokenType"`
	AppliesTo string `xml:"AppliesTo"`
	LifeTime string `xml:"LifeTime"`
	ReSeToken RequestedSecurityToken `xml:"RequestedSecurityToken"`
}

type RequestedSecurityToken struct {
	BinarySecurityToken string `xml:"BinarySecurityToken"`
}

type EdgeResp struct {
	SkypeToken string `json:"skypetoken"`
	ExpiresIn int32 `json:"expiresIn"`
	SkypeId string `json:"skypeid"`
	SignInName string `json:"signinname"`
	Anid string `json:"anid"`
	Status struct{
		Code int32 `json:"code"`
		Text string `json:"text"`
	} `json:"status"`
}

/**
获得用户的id
*/
func (c *Conn) GetUserId(skypetoken string) (err error) {
	req := Request{
		timeout: 30,
	}
	headers := map[string]string{
		"x-skypetoken": skypetoken,
	}
	body, err := req.HttpGetWitHeaderAndCookiesJson(fmt.Sprintf("%s/users/self/profile", API_USER), nil, "", nil, headers)
	//解析参数
	if err != nil {
		return errors.New("get userId err")
	}

	userProfile := UserProfile{}
	json.Unmarshal([]byte(body), &userProfile)
	c.UserProfile = &userProfile
	return
}

/**
    Request a new registration token using a current Skype token.
	Args:
		skypeToken (str): existing Skype token
	Returns:
		(str, datetime.datetime, str, SkypeEndpoint) tuple: registration token, associated expiry if known,
															resulting endpoint hostname, endpoint if provided
	Raises:
		.SkypeAuthException: if the login request is rejected
		.SkypeApiExce`ption: if the login form can't be processed
 * Value used for the `ConnInfo` header of the request for the registration token.
*/
func (c *Conn) SkypeRegistrationTokenProvider(skypeToken string) (err error) {
	secs := strconv.Itoa(int(time.Now().Unix()))
	lockAndKeyResponse := getMac256Hash(secs)
	LockAndKey := "appId=" + SKYPEWEB_LOCKANDKEY_APPID + "; time=" + secs + "; lockAndKeyResponse=" + lockAndKeyResponse
	req := Request{
		timeout: 30,
	}
	header := map[string]string{
		"Authentication":   "skypetoken=" + skypeToken,
		"LockAndKey":       LockAndKey,
		"BehaviorOverride": "redirectAs404",
	}
	data := map[string]interface{}{
		"endpointFeatures": "Agent",
	}
	params, _ := json.Marshal(data)
	registrationTokenStr, location, err := req.HttpPostRegistrationToken(c.LoginInfo.LocationHost+"/v1/users/"+DEFAULT_USER+"/endpoints", string(params), header)
	if err != nil {
		return
	}
	locationArr := strings.Split(location, "/v1")
	c.storeInfo(registrationTokenStr, c.LoginInfo.LocationHost)
	if locationArr[0] != c.LoginInfo.LocationHost {
		newRegistrationToken, _, err := req.HttpPostRegistrationToken(location, string(params), header)
		if err == nil {
			c.storeInfo(newRegistrationToken, locationArr[0])
		}
	}
	return
}

func (c *Conn) storeInfo(registrationTokenStr string, locationHost string) {
	regArr := strings.Split(registrationTokenStr, ";")
	registrationToken := ""
	registrationExpires := ""
	if len(regArr) > 0 {
		for _, v := range regArr {
			v = strings.Replace(v, " ", "", -1)
			if len(v) > 0 {
				if strings.Index(v, "registrationToken=") > -1 {
					vv := strings.Split(v, "registrationToken=")
					registrationToken = vv[1]
				} else {
					vv := strings.Split(v, "=")
					if vv[0] == "expires" {
						registrationExpires = vv[1]
					}
					if vv[0] == "endpointId" {
						if vv[1] != "" {
							c.LoginInfo.EndpointId = vv[1]
						}
					}
				}
			}
		}
	}
	c.LoginInfo.LocationHost = locationHost
	c.LoginInfo.RegistrationToken = registrationToken
	c.LoginInfo.RegistrationExpires = registrationExpires
	c.LoggedIn = true
	if strings.Index(registrationTokenStr, "endpointId=") == -1 {
		registrationTokenStr = registrationTokenStr + "; endpointId=" + c.LoginInfo.EndpointId
	} else {
		c.LoginInfo.RegistrationTokenStr = registrationTokenStr
	}
	return
}

func (c *Conn) Subscribes() {
	req := Request{
		timeout: 60,
	}

	subscribePath := c.SubscribePath()
	data := map[string]interface{}{
		"interestedResources": []string{
			"/v1/threads/ALL",
			"/v1/users/ME/contacts/ALL",
			"/v1/users/ME/conversations/ALL/messages",
			"/v1/users/ME/conversations/ALL/properties",
		},
		"template":    "raw",
		"channelType": "httpLongPoll",
	}
	header := map[string]string{
		"Authentication":    "skypetoken=" + c.LoginInfo.SkypeToken,
		"RegistrationToken": c.LoginInfo.RegistrationTokenStr,
		"BehaviorOverride":  "redirectAs404",
	}
	params, _ := json.Marshal(data)
	_, err, _ := req.request("post", subscribePath, strings.NewReader(string(params)), nil, header)
	if err != nil {
		fmt.Println("Subscribes request err: ", err)
	}
}

/**
@params
ids []8:xxxxxx
 */
func (c *Conn) SubscribeUsers(ids []string) {
	if len(ids) < 1 {
		return
	}

	req := Request{
		timeout: 60,
	}
	subscribePath := c.SubscribePath() + "/0?name=interestedResources"
	data := map[string][]string{
		"interestedResources": {
			"/v1/threads/ALL",
			//"/v1/users/ME/contacts/ALL",
			"/v1/users/ME/conversations/ALL/messages",
			"/v1/users/ME/conversations/ALL/properties",
		},
	}
	for _, id := range ids {
		subStr := "/v1/users/ME/contacts/" + id
		data["interestedResources"] = append(data["interestedResources"], subStr)
	}

	header := map[string]string{
		"Authentication":    "skypetoken=" + c.LoginInfo.SkypeToken,
		"RegistrationToken": c.LoginInfo.RegistrationTokenStr,
		"BehaviorOverride":  "redirectAs404",
	}
	params, _ := json.Marshal(data)
	_, err, _ := req.request("PUT", subscribePath, strings.NewReader(string(params)), nil, header)
	if err != nil {
		fmt.Println("SubscribeUsers request err: ", err)
	}
}

func (c *Conn) Poll() {
	req := Request{
		timeout: 60,
	}

	for {
		if c.LoggedIn == false {
			break
		}
		pollPath := c.PollPath()
		header := map[string]string{
			"Authentication":    "skypetoken=" + c.LoginInfo.SkypeToken,
			"RegistrationToken": c.LoginInfo.RegistrationTokenStr,
			"BehaviorOverride":  "redirectAs404",
		}
		data := map[string]interface{}{
			"endpointFeatures": "Agent",
		}
		params, _ := json.Marshal(data)
		body, err, _ := c.request(req, "post", pollPath, strings.NewReader(string(params)), nil, header)
		if c.LoggedIn == false {
			break
		}
		if err != nil {
			fmt.Println("poller err: ", err)
		}
		fmt.Println("poller body: ", body)
		if body != "" {
			var bodyContent struct {
				EventMessages []Conversation `json:"eventMessages"`
			}
			err = json.Unmarshal([]byte(body), &bodyContent)
			if err != nil {
				fmt.Println("json.Unmarshal poller body err: ", err)
			}
			if len(bodyContent.EventMessages) > 0 {
				for _, message := range bodyContent.EventMessages {
					if message.Type == "EventMessage" {
						c.handle(message)
					}
				}
			}
		}
	}
}

func (c *Conn) PollPath() (path string) {
	path = c.LoginInfo.LocationHost + "/v1/users/ME/endpoints/" + c.LoginInfo.EndpointId + "/subscriptions/0/poll"
	return
}

func (c *Conn) SubscribePath() (path string) {
	path = c.LoginInfo.LocationHost + "/v1/users/ME/endpoints/" + c.LoginInfo.EndpointId + "/subscriptions"
	return
}

func (c *Conn) getToken(t string) (err error) {

	// # Now pass the login credentials over.
	paramsMap := url.Values{}
	paramsMap.Set("client_id", "578134")
	paramsMap.Set("redirect_uri", "https://web.skype.com")

	req := Request{
		timeout: 30,
	}
	data := map[string]interface{}{
		"t":            t,
		"client_id":    "578134",
		"oauthPartner": "999",
		"site_name":    "lw.skype.com",
		"redirect_uri": "https://web.skype.com",
	}
	query, _ := json.Marshal(data)
	_, err, _, token, expires := req.HttpPostBase(fmt.Sprintf("%s/microsoft?%s", API_LOGIN, gurl.BuildQuery(paramsMap)), string(query))
	c.LoginInfo = &Session{
		SkypeToken:   token,
		SkypeExpires: expires,
	}
	if err != nil {
		return
	}
	if token == "" {
		return errors.New("can't get token")
	}
	return
}

func (c *Conn) sendCred(username, pwd, MSPRequ, MSPOK, PPFT string) (body string, err error, tValue string) {
	paramsMap := url.Values{}
	paramsMap.Set("wa", "wsignin1.0")
	paramsMap.Set("wp", "MBI_SSL")
	paramsMap.Set("wreply", "https://lw.skype.com/login/oauth/proxy?client_id=578134&site_name=lw.skype.com&redirect_uri=https%3A%2F%2Fweb.skype.com%2F")
	req := Request{
		timeout: 30,
	}
	cookies := map[string]string{
		"MSPRequ": MSPRequ,
		"MSPOK":   MSPOK,
		"CkTst":   "G" + API_MSACC + strconv.Itoa(time.Now().Second() * 1000),
	}
	formParams := url.Values{}
	formParams.Add("login", username)
	formParams.Add("passwd", pwd)
	formParams.Add("PPFT", PPFT)
	//formParams.Add("loginoptions", "3")

	query, _ := json.Marshal(formParams)
	body, err, _, tValue = req.HttpPostWithParamAndDataWithIdt(fmt.Sprintf("%s/ppsecure/post.srf", API_MSACC), paramsMap, string(query), cookies, "t")
	return
}

func (c *Conn) sendCred1(paramsMap url.Values, username, password, PPFT string, cookies map[string]string) (opid string, err error) {
	req := Request{
		timeout: 30,
	}

	formParams := url.Values{}
	formParams.Add("login", username)
	formParams.Add("passwd", password)
	formParams.Add("PPFT", PPFT)
	formParams.Add("loginoptions", "3")
	//cookies["CkTst"] =  "G" + API_MSACC + strconv.Itoa(time.Now().Second() * 1000)
	formData, _ := json.Marshal(formParams)
	reqUrl := fmt.Sprintf("%s?%s", fmt.Sprintf("%s/ppsecure/post.srf", API_MSACC), gurl.BuildQuery(paramsMap))
	body, err, _ := req.request("POST", reqUrl, strings.NewReader(string(formData)), cookies, nil)
	if err != nil {
		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return
	}
	doc.Find("form").Each(func(_ int, s *goquery.Selection) {
		nameValue, _ := s.Attr("name")
		actionValue, _ := s.Attr("action")
		if nameValue == "fmHF" {
			uslArr := strings.Split("?", actionValue)
			err = errors.New(fmt.Sprintf("Account action required (%s), login with a web browser first", uslArr[0]))
			return
		}
	})

	r := regexp.MustCompile(`opid=([A-Z0-9]+)`)
	res := find(body, r)
	opid = res[0][1]
	return
}

func (c *Conn) sendOpid(paramsMap url.Values, PPFT, opid string, cookies map[string]string, username, password string) (t string, err error) {
	req := Request{
		timeout: 30,
	}
	formParams := url.Values{}
	formParams.Add("login", username)
	formParams.Add("passwd", password)
	formParams.Add("opid", opid)
	formParams.Add("site_name", "lw.skype.com")
	formParams.Add("oauthPartner", "999")
	formParams.Add("client_id", "578134")
	formParams.Add("redirect_uri", "https://web.skype.com")
	formParams.Add("PPFT", PPFT)
	formParams.Add("type", "28")

	formData, _ := json.Marshal(formParams)
	reqUrl := fmt.Sprintf("%s?%s", fmt.Sprintf("%s/ppsecure/post.srf", API_MSACC), gurl.BuildQuery(paramsMap))
	body, err, _ := req.request("POST", reqUrl, strings.NewReader(string(formData)), cookies, nil)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return
	}
	doc.Find("input").Each(func(_ int, s *goquery.Selection) {
		idt, _ := s.Attr("id")
		if idt == "t" {
			t, _ = s.Attr("value")
		}
	})
	return
}

func find(htm string, re *regexp.Regexp) [][]string {
	imgs := re.FindAllStringSubmatch(htm, -1)
	return imgs
}

func (c *Conn) getParams() (MSPRequ, MSPOK, PPFT string, err error) {
	params := url.Values{}
	params.Set("client_id", "578134")
	params.Set("redirect_uri", "https://web.skype.com")
	req := Request{
		timeout: 30,
	}
	//第一步, 302重定向跳转
	//fmt.Println(fmt.Sprintf("%s/oauth/microsoft", API_LOGIN))
	redirectUrl, err, _ := req.HttpGetJson(fmt.Sprintf("%s/oauth/microsoft", API_LOGIN), params)
	//请求跳转的链接
	if err != nil {
		return "", "", "", errors.New("error redirect url at first step")
	}
	loginSpfParam := url.Values{}
	loginSrfBody, err, loginSrfResponse := req.HttpGetJsonBackResponse(redirectUrl, loginSpfParam)
	//从 内容中匹配出来  PPFT
	buf := `<input.*?name="PPFT".*?value="(.*?)` + `\"`
	reg := regexp.MustCompile(buf)
	ppfts := reg.FindAllString(loginSrfBody, -1)
	var ppftByte []byte
	var ppftStr string
	if len(ppfts) > 0 {
		for k, v := range ppfts {
			if k == 0 {
				ppftbbf := `value=".*?"`
				ppftreg := regexp.MustCompile(ppftbbf)
				ppftsppft := ppftreg.FindAllString(v, -1)
				ppftByte = []byte(ppftsppft[0])[7:]
				ppftStr = string(ppftByte[0 : len(ppftByte)-1])
			}
		}
	}
	for _, v := range loginSrfResponse.Cookies() {
		if v.Name == "MSPRequ" {
			MSPRequ = v.Value
		}
		if v.Name == "MSPOK" {
			MSPOK = v.Value
		}
	}
	//发送账号密码  判定是否存在次账号
	return MSPRequ, MSPOK, ppftStr, nil
}

func (c *Conn) request(req Request, method string, reqUrl string, reqBody io.Reader, cookies map[string]string, header map[string]string) (body string, err error, status int)  {
	body, err, status = req.request(method, reqUrl, reqBody, cookies, header)
	fmt.Println("request StatusCode:", status)
	if status == 401 {
		// skypetoken is invalid
		// TODO refresh skypetoken and more
		c.LoggedIn = false

	} else if status == 404 || status == 729 {
		// refresh registrationtoken
		_ = c.SkypeRegistrationTokenProvider(c.LoginInfo.SkypeToken)
	}
	return
}
