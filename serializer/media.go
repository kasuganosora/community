package serializer

import (
	"fmt"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"strconv"
)

// MediaAttachmentSerializer 媒体附件序列化器.
type MediaAttachmentSerializer struct{}

// MediaAttachmentResponse 媒体附件响应.
type MediaAttachmentResponse struct {
	Id               string               `json:"id"`
	Type             string               `json:"type"`
	Url              string               `json:"url"`
	PreviewUrl       string               `json:"preview_url"`
	RemoteUrl        *string              `json:"remote_url"`
	PreviewRemoteUrl *string              `json:"preview_remote_url"`
	TextUrl          *string              `json:"text_url"`
	Meta             *MediaAttachmentMeta `json:"meta"`
	Description      string               `json:"description"`
	Blurhash         string               `json:"blurhash"`
}

// MediaAttachmentMeta 媒体元数据.
type MediaAttachmentMeta struct {
	Focus    *MediaAttachmentFocus    `json:"focus,omitempty"`
	Original *MediaAttachmentOriginal `json:"original,omitempty"`
	Small    *MediaAttachmentSmall    `json:"small,omitempty"`
}

// MediaAttachmentFocus 焦点位置.
type MediaAttachmentFocus struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// MediaAttachmentOriginal 原始尺寸.
type MediaAttachmentOriginal struct {
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Size     string  `json:"size,omitempty"`
	Aspect   float64 `json:"aspect,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Bitrate  int     `json:"bitrate,omitempty"`
}

// MediaAttachmentSmall 缩略图尺寸.
type MediaAttachmentSmall struct {
	Width  int     `json:"width,omitempty"`
	Height int     `json:"height,omitempty"`
	Size   string  `json:"size,omitempty"`
	Aspect float64 `json:"aspect,omitempty"`
}

// ToMediaAttachment 转换为 Mastodon 格式的媒体附件.
func ToMediaAttachment(media *models.MediaAttachment) (*MediaAttachmentResponse, error) {
	if media == nil {
		return nil, nil //nolint:nilnil
	}

	// 获取基础 URL
	baseURL := config.C.AppBaseURL

	// 构建文件 URL
	url := fmt.Sprintf("%s/media/%s/%s", baseURL, strconv.FormatUint(media.Id, 10), media.FileName)

	// 构建缩略图 URL
	var previewUrl string
	if media.ThumbnailPath != nil && *media.ThumbnailPath != "" {
		previewUrl = fmt.Sprintf("%s/media/%s/thumbnail/%s", baseURL, strconv.FormatUint(media.Id, 10), *media.ThumbnailPath)
	} else {
		previewUrl = url // 如果没有缩略图，使用原图
	}

	// 构建元数据
	meta := &MediaAttachmentMeta{
		Focus: &MediaAttachmentFocus{
			X: media.FocusX,
			Y: media.FocusY,
		},
	}

	// 添加原始尺寸信息
	if media.Width != nil && media.Height != nil && *media.Width > 0 && *media.Height > 0 {
		meta.Original = &MediaAttachmentOriginal{
			Width:  *media.Width,
			Height: *media.Height,
			Size:   fmt.Sprintf("%dx%d", *media.Width, *media.Height),
			Aspect: float64(*media.Width) / float64(*media.Height),
		}

		// 添加缩略图尺寸（假设为 400x400）
		meta.Small = &MediaAttachmentSmall{
			Width:  400,
			Height: int(float64(400) * float64(*media.Height) / float64(*media.Width)),
			Size:   fmt.Sprintf("400x%d", int(float64(400)*float64(*media.Height)/float64(*media.Width))),
			Aspect: float64(*media.Width) / float64(*media.Height),
		}
	}

	// 添加视频/音频时长
	if media.Duration != nil && *media.Duration > 0 {
		if meta.Original == nil {
			meta.Original = &MediaAttachmentOriginal{}
		}

		meta.Original.Duration = *media.Duration
	}

	return &MediaAttachmentResponse{
		Id:               strconv.FormatUint(media.Id, 10),
		Type:             string(media.Type),
		Url:              url,
		PreviewUrl:       previewUrl,
		RemoteUrl:        nil,
		PreviewRemoteUrl: nil,
		TextUrl:          nil,
		Meta:             meta,
		Description:      media.Description,
		Blurhash:         media.Blurhash,
	}, nil
}

// ToMediaAttachmentList 转换为 Mastodon 格式的媒体附件列表.
func ToMediaAttachmentList(medias models.MediaAttachments) ([]*MediaAttachmentResponse, error) {
	if len(medias) == 0 {
		return []*MediaAttachmentResponse{}, nil
	}

	results := make([]*MediaAttachmentResponse, 0, len(medias))

	for _, media := range medias {
		serialized, err := ToMediaAttachment(media)
		if err != nil {
			return nil, err
		}

		if serialized != nil {
			results = append(results, serialized)
		}
	}

	return results, nil
}
