package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

// Configuración
const maxFileSize = 10 << 20 // 10MB

// Rate limiter global
var limiter = rate.NewLimiter(5, 10)

func main() {
	e := echo.New()

	// CORS
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{
			"http://localhost:5173",
			"https://tu-frontend.vercel.app",
		},
		AllowMethods: []string{echo.POST, echo.OPTIONS},
		AllowHeaders: []string{echo.HeaderContentType},
		ExposeHeaders: []string{
			"X-Original-Size",
			"X-Compressed-Size",
			"X-Reduction",
		},
	}))

	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "API running")
	})

	e.POST("/compress", compressHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	e.Logger.Fatal(e.Start(":" + port))
}

func compressHandler(c echo.Context) error {

	if !limiter.Allow() {
		return c.JSON(http.StatusTooManyRequests, map[string]string{
			"error": "Too many requests",
		})
	}

	file, err := c.FormFile("image")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "No file"})
	}

	if file.Size > maxFileSize {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "File too large (max 10MB)",
		})
	}

	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	buffer := make([]byte, 512)
	_, err = src.Read(buffer)
	if err != nil {
		return err
	}

	mimeType := http.DetectContentType(buffer)
	if !strings.HasPrefix(mimeType, "image/") {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid MIME type",
		})
	}

	src.Seek(0, io.SeekStart)

	img, format, err := image.Decode(src)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid image format",
		})
	}

	var compressed bytes.Buffer

	switch format {
	case "jpeg":
		err = jpeg.Encode(&compressed, img, &jpeg.Options{Quality: 60})
	case "png":
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		err = encoder.Encode(&compressed, img)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Only JPEG and PNG supported",
		})
	}

	if err != nil {
		return err
	}

	reduction := 100 - (float64(compressed.Len())/float64(file.Size))*100

	// Headers personalizados
	c.Response().Header().Set("X-Original-Size", fmt.Sprintf("%d", file.Size))
	c.Response().Header().Set("X-Compressed-Size", fmt.Sprintf("%d", compressed.Len()))
	c.Response().Header().Set("X-Reduction", fmt.Sprintf("%.2f", reduction))

	// Content-Type correcto
	if format == "png" {
		return c.Blob(http.StatusOK, "image/png", compressed.Bytes())
	}

	return c.Blob(http.StatusOK, "image/jpeg", compressed.Bytes())
}
