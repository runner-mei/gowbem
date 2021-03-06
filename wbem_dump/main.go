package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/runner-mei/gowbem"
)

var (
	schema    = flag.String("scheme", "", "url 的 scheme, 当值为空时根据 port 的值自动选择: 5988 = http, 5989 = https, 缺省值为 http")
	host      = flag.String("host", "192.168.1.157", "主机的 IP 地址")
	port      = flag.String("port", "0", "主机上 CIM 服务的端口号, 当值为 0 时根据 schema 的值自动选择: http = 5988, https = 5989 ")
	path      = flag.String("path", "/cimom", "CIM 服务访问路径")
	namespace = flag.String("namespace", "", "CIM 的命名空间, 缺省值： root/cimv2")
	classname = flag.String("class", "", "CIM 的的类名")
	onlyclass = flag.Bool("onlyclass", false, "仅列出类名")

	username     = flag.String("username", "root", "用户名")
	userpassword = flag.String("password", "root", "用户密码")
	output       = flag.String("output", "", "结果的输出目录, 缺省值为当前目录")
	debug        = flag.Bool("debug", true, "是不是在调试")
)

func createURI() *url.URL {
	return &url.URL{
		Scheme: *schema,
		User:   url.UserPassword(*username, *userpassword),
		Host:   *host + ":" + *port,
		Path:   *path,
	}
}

func main() {
	flag.Usage = func() {
		fmt.Println("使用方法： wbem_dump -host=192.168.1.157 -port=5988 -username=root -password=rootpwd\r\n" +
			"可用选项")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *output == "" {
		*output = "./" + *host
	}

	if err := os.MkdirAll(*output, 666); err != nil && !os.IsExist(err) {
		log.Fatalln(err)
	}

	if *debug {
		gowbem.SetDebugProvider(&gowbem.FileDebugProvider{Path: *output})
	}

	if *port == "" || *port == "0" {
		switch *schema {
		case "http":
			*port = "5988"
		case "https":
			*port = "5989"
		case "":
			*schema = "http"
			*port = "5988"
		}
	} else if *schema == "" {
		switch *port {
		case "5988":
			*schema = "http"
		case "5989":
			*schema = "https"
		}
	}

	c, e := gowbem.NewClientCIMXML(createURI(), true)
	if nil != e {
		log.Fatalln("连接失败，", e)
	}

	if *classname != "" && *namespace != "" {
		instancePaths := make(map[string]error, 1024)
		dumpClass(c, *namespace, *classname, instancePaths)
		return
	}

	var namespaces []string
	timeCtx, _ := context.WithTimeout(context.Background(), 30*time.Second)

	if "" == *namespace {
		var err error
		namespaces, err = c.EnumerateNamespaces(timeCtx, []string{"root/cimv2"}, 10*time.Second, nil)
		if nil != err {
			log.Fatalln("连接失败，", err)
		}
	} else {
		namespaces = []string{*namespace}
	}

	fmt.Println("命令空间有：", namespaces)
	for _, ns := range namespaces {
		fmt.Println("开始处理", ns)
		dumpNS(c, ns)
	}
	if len(namespaces) == 0 {
		fmt.Println("导出失败！")
		os.Exit(1)
		return
	}
	fmt.Println("导出成功！")
}

func dumpNS(c *gowbem.ClientCIMXML, ns string) {
	qualifiers, e := c.EnumerateQualifierTypes(context.Background(), ns)
	if nil != e {
		fmt.Println("枚举 QualifierType 失败，", e)
		// return
	}

	if len(qualifiers) > 0 {
		nsPath := strings.Replace(ns, "/", "#", -1)
		nsPath = strings.Replace(nsPath, "\\", "@", -1)

		/// @begin 将 Qualifier 定义写到文件
		filename := filepath.Join(*output, nsPath, "qa.xml")
		if err := os.MkdirAll(filepath.Join(*output, nsPath), 666); err != nil && !os.IsExist(err) {
			log.Fatalln(err)
		}

		var sb bytes.Buffer
		sb.WriteString(`<?xml version="1.0"?>
<CIM CIMVERSION="2.0" DTDVERSION="2.0">
<DECLARATION>
<DECLGROUP>`)
		for idx := range qualifiers {
			sb.WriteString("\r\n")
			sb.WriteString(`<VALUE.OBJECT>`)
			sb.WriteString("\r\n")

			bs, err := xml.MarshalIndent(qualifiers[idx], "", "  ")
			if err != nil {
				log.Fatalln(err)
			}
			sb.Write(bs)

			sb.WriteString("\r\n")
			sb.WriteString(`</VALUE.OBJECT>`)
			sb.WriteString("\r\n")
		}

		sb.WriteString(`</DECLGROUP>
</DECLARATION>
</CIM>`)

		if err := ioutil.WriteFile(filename, sb.Bytes(), 666); err != nil {
			log.Fatalln(err)
		}
		/// @end
	}

	classNames, e := c.EnumerateClassNames(context.Background(), ns, "", true)
	if nil != e {
		if !gowbem.IsErrNotSupported(e) && !gowbem.IsEmptyResults(e) {
			fmt.Println("枚举类名失败，", e)
		}
		return
	}
	if 0 == len(classNames) {
		fmt.Println("没有类定义？，")
		return
	}

	if *onlyclass {
		fmt.Println("命令空间 ", ns, "下有：")
		for _, className := range classNames {
			fmt.Println(className)
		}
		return
	}

	instancePaths := make(map[string]error, 1024)
	fmt.Println("命令空间 ", ns, "下有：", classNames)

	for _, className := range classNames {
		timeCtx, _ := context.WithTimeout(context.Background(), 30*time.Second)
		class, err := c.GetClass(timeCtx, ns, className, true, true, true, nil)
		if err != nil {
			fmt.Println("取数名失败 - ", err)
		}

		nsPath := strings.Replace(ns, "/", "#", -1)
		nsPath = strings.Replace(nsPath, "\\", "@", -1)

		/// @begin 将类定义写到文件
		filename := filepath.Join(*output, nsPath, className+".xml")
		if err := os.MkdirAll(filepath.Join(*output, nsPath), 666); err != nil && !os.IsExist(err) {
			log.Fatalln(err)
		}
		if err := ioutil.WriteFile(filename, []byte(class), 666); err != nil {
			log.Fatalln(err)
		}
		/// @end

		dumpClass(c, ns, className, instancePaths)
	}

	for key, err := range instancePaths {
		if err != nil {
			fmt.Println(key, "获取失败:", err)
		}
	}
}

func dumpClass(c *gowbem.ClientCIMXML, ns, className string, instancePaths map[string]error) {
	nsPath := strings.Replace(ns, "/", "#", -1)
	nsPath = strings.Replace(nsPath, "\\", "@", -1)

	timeCtx, _ := context.WithTimeout(context.Background(), 30*time.Second)
	instanceNames, err := c.EnumerateInstanceNames(timeCtx, ns, className)
	if err != nil {

		/// @begin 将类定义写到文件
		classPath := filepath.Join(*output, nsPath)
		if e := os.MkdirAll(classPath, 666); e != nil && !os.IsExist(e) {
			log.Fatalln(e)
		}
		if e := ioutil.WriteFile(filepath.Join(classPath, "error.txt"), []byte(err.Error()), 666); e != nil {
			log.Fatalln(e)
		}

		fmt.Println(className, 0, err)

		if !gowbem.IsErrNotSupported(err) && !gowbem.IsEmptyResults(err) {
			fmt.Println(fmt.Sprintf("%T %v", err, err))
		}
		return
	}
	fmt.Println(className, len(instanceNames))

	if len(instanceNames) == 0 {
		return
	}

	/// @begin 将类定义写到文件
	classPath := filepath.Join(*output, nsPath, className)
	if err := os.MkdirAll(classPath, 666); err != nil && !os.IsExist(err) {
		log.Fatalln(err)
	}
	var buf bytes.Buffer
	for _, instanceName := range instanceNames {
		buf.WriteString(instanceName.String())
		buf.WriteString("\r\n")
	}
	if err := ioutil.WriteFile(filepath.Join(classPath, "instances.txt"), buf.Bytes(), 666); err != nil {
		log.Fatalln(err)
	}
	/// @end

	for idx, instanceName := range instanceNames {
		if _, exists := instancePaths[instanceName.String()]; exists {
			continue
		}

		timeCtx, _ := context.WithTimeout(context.Background(), 30*time.Second)
		instance, err := c.GetInstanceByInstanceName(timeCtx, ns, instanceName, false, true, true, nil)
		if err != nil {
			instancePaths[instanceName.String()] = err

			if !gowbem.IsErrNotSupported(err) && !gowbem.IsEmptyResults(err) {
				fmt.Println(fmt.Sprintf("%T %v", err, err))
			}
			continue
		}

		/// @begin 将类定义写到文件
		bs, err := xml.MarshalIndent(instance, "", "  ")
		if err != nil {
			log.Fatalln(err)
		}

		subclassPath := filepath.Join(*output, nsPath, instanceName.GetClassName())
		if err := os.MkdirAll(subclassPath, 666); err != nil && !os.IsExist(err) {
			log.Fatalln(err)
		}

		if err := ioutil.WriteFile(filepath.Join(subclassPath, "instance_"+strconv.Itoa(idx)+".xml"), bs, 666); err != nil {
			log.Fatalln(err)
		}
		/// @end

		instancePaths[instanceName.String()] = nil

		// fmt.Println()
		// fmt.Println()
		// fmt.Println(instanceName.String())
		//for _, k := range instance.GetProperties() {
		//	fmt.Println(k.GetName(), k.GetValue())
		//}
	}
}
