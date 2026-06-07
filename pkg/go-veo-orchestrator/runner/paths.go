package runner

import (
	"path"

	"github.com/shouni/go-utils/urlpath"
)

const (
	characterDesignDir   = "character"
	defaultImageDir      = "images"
	defaultKeyframeName  = "keyframe.png"
	defaultVideoMetaJSON = "video_music_meta.json"
)

func defaultKeyframePath() string {
	return path.Join(defaultImageDir, defaultKeyframeName)
}

func resolveOutputPath(baseDir, fileName string) (string, error) {
	return urlpath.ResolvePath(baseDir, fileName)
}

func resolveBaseURL(rawPath string) string {
	return urlpath.ResolveBaseDir(rawPath)
}

func generateIndexedPath(basePath string, index int) (string, error) {
	return urlpath.GenerateIndexedPath(basePath, index)
}
