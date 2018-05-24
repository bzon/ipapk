package ipapk

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DHowett/go-plist"
	"github.com/andrianbdn/iospng"
	"github.com/shogo82148/androidbinary"
	"github.com/shogo82148/androidbinary/apk"
)

var reInfoPlist = regexp.MustCompile(`Payload/[^/]+/Info\.plist`)

const (
	iosExt     = ".ipa"
	androidExt = ".apk"
)

type AppInfo struct {
	Name     string
	BundleId string
	Version  string
	Build    string
	Icon     image.Image
	Size     int64
}

type androidManifest struct {
	Package     string `xml:"package,attr"`
	VersionName string `xml:"versionName,attr"`
	VersionCode string `xml:"versionCode,attr"`
}

type iosPlist struct {
	CFBundleName         string `plist:"CFBundleName"`
	CFBundleDisplayName  string `plist:"CFBundleDisplayName"`
	CFBundleVersion      string `plist:"CFBundleVersion"`
	CFBundleShortVersion string `plist:"CFBundleShortVersionString"`
	CFBundleIdentifier   string `plist:"CFBundleIdentifier"`
}

func NewAppParser(name string) (*AppInfo, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("failed opening file: %v: %v", name, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed getting file stat: %v", err)
	}

	reader, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("failed reading zip file: %v", err)
	}

	var xmlFile, plistFile, iosIconFile *zip.File
	for _, f := range reader.File {
		switch {
		case f.Name == "AndroidManifest.xml":
			xmlFile = f
		case reInfoPlist.MatchString(f.Name):
			plistFile = f
		case strings.Contains(f.Name, "AppIcon60x60"):
			iosIconFile = f
		}
	}

	ext := filepath.Ext(stat.Name())

	if ext == androidExt {
		info, err := parseApkFile(xmlFile)
		icon, label, err := parseApkIconAndLabel(name)
		info.Name = label
		info.Icon = icon
		info.Size = stat.Size()
		return info, err
	}

	if ext == iosExt {
		info, err := parseIpaFile(plistFile)
		if err != nil {
			return nil, err
		}
		icon, err := parseIpaIcon(iosIconFile)
		if err != nil {
			return nil, fmt.Errorf("failed parsing ipa icon file: %v", err)
		}
		info.Icon = icon
		info.Size = stat.Size()
		return info, err
	}

	return nil, errors.New("unknown platform")
}

func parseAndroidManifest(xmlFile *zip.File) (*androidManifest, error) {
	rc, err := xmlFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	buf, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	xmlContent, err := androidbinary.NewXMLFile(bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}

	manifest := new(androidManifest)
	decoder := xml.NewDecoder(xmlContent.Reader())
	if err := decoder.Decode(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func parseApkFile(xmlFile *zip.File) (*AppInfo, error) {
	if xmlFile == nil {
		return nil, errors.New("AndroidManifest.xml is not found")
	}

	manifest, err := parseAndroidManifest(xmlFile)
	if err != nil {
		return nil, err
	}

	info := new(AppInfo)
	info.BundleId = manifest.Package
	info.Version = manifest.VersionName
	info.Build = manifest.VersionCode

	return info, nil
}

func parseApkIconAndLabel(name string) (image.Image, string, error) {
	pkg, err := apk.OpenFile(name)
	if err != nil {
		return nil, "", err
	}
	defer pkg.Close()

	icon, _ := pkg.Icon(&androidbinary.ResTableConfig{
		Density: 720,
	})
	if icon == nil {
		return nil, "", errors.New("Icon is not found")
	}

	label, _ := pkg.Label(nil)

	return icon, label, nil
}

func parseIpaFile(plistFile *zip.File) (*AppInfo, error) {
	if plistFile == nil {
		return nil, errors.New("info.plist is not found")
	}

	rc, err := plistFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed opening plist file: %v", err)
	}
	defer rc.Close()

	buf, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed reading plist data: %v", err)
	}

	p := new(iosPlist)
	decoder := plist.NewDecoder(bytes.NewReader(buf))
	if err := decoder.Decode(p); err != nil {
		return nil, fmt.Errorf("failed decoding plist data: %v", err)
	}

	info := new(AppInfo)
	if p.CFBundleDisplayName == "" {
		info.Name = p.CFBundleName
	} else {
		info.Name = p.CFBundleDisplayName
	}
	info.BundleId = p.CFBundleIdentifier
	info.Version = p.CFBundleShortVersion
	info.Build = p.CFBundleVersion

	return info, nil
}

func parseIpaIcon(iconFile *zip.File) (image.Image, error) {

	if iconFile == nil {
		return nil, errors.New("Icon is not found")
	}

	rc, err := iconFile.Open()
	if err != nil {
		return nil, fmt.Errorf("Failed opening icon file: %v", err)
	}
	defer rc.Close()

	var w bytes.Buffer
	err = iospng.PngRevertOptimization(rc, &w)
	// BUG(@bzon): can't read sample ipa built from
	// from https://github.com/browserstack/xcuitest-sample-browserstack
	if err == iospng.ErrImageData {
		image, _ := png.Decode(bytes.NewReader(w.Bytes()))
		return image, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed png revert optimization: %v", err)
	}
	image, err := png.Decode(bytes.NewReader(w.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("failed decoding png: %v", err)
	}
	return image, nil
}
