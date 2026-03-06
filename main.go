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
	"runtime"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

// Configuración
const maxFileSize = 10 << 20 // 10MB

// Rate limiter global
var limiter = rate.NewLimiter(5, 10)

// Pool de buffers para reutilización
var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

func main() {
	e := echo.New()

	// 🔥 Middleware primero
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{echo.POST, echo.OPTIONS},
		AllowHeaders: []string{echo.HeaderContentType},
		ExposeHeaders: []string{
			"X-Original-Size",
			"X-Compressed-Size",
			"X-Reduction",
		},
	}))

	e.POST("/compress", compressHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	e.Logger.Fatal(e.Start(":" + port))
}

// Estructura para pasar resultados entre goroutines
type compressionResult struct {
	format     string
	compressed *bytes.Buffer
	err        error
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

	// Compresión paralela con goroutines
	resultChan := make(chan compressionResult, 1)
	go compressImageParallel(img, format, resultChan)

	result := <-resultChan

	if result.err != nil {
		return result.err
	}

	defer bufferPool.Put(result.compressed)

	reduction := 100 - (float64(result.compressed.Len())/float64(file.Size))*100

	// Headers personalizados
	c.Response().Header().Set("X-Original-Size", fmt.Sprintf("%d", file.Size))
	c.Response().Header().Set("X-Compressed-Size", fmt.Sprintf("%d", result.compressed.Len()))
	c.Response().Header().Set("X-Reduction", fmt.Sprintf("%.2f", reduction))

	mimeTypeResponse := "image/jpeg"
	if result.format == "png" {
		mimeTypeResponse = "image/png"
	}

	return c.Blob(http.StatusOK, mimeTypeResponse, result.compressed.Bytes())
}

// Comprime imagen en goroutine usando múltiples workers
func compressImageParallel(img image.Image, format string, result chan compressionResult) {
	compressed := bufferPool.Get().(*bytes.Buffer)
	compressed.Reset()

	numWorkers := runtime.NumCPU()

	switch format {
	case "jpeg":
		// JPEG: usar workers para procesar en paralelo
		err := compressJPEGParallel(compressed, img, numWorkers)
		result <- compressionResult{format: "jpeg", compressed: compressed, err: err}

	case "png":
		// PNG: usar workers para procesar en paralelo
		err := compressPNGParallel(compressed, img, numWorkers)
		result <- compressionResult{format: "png", compressed: compressed, err: err}

	default:
		result <- compressionResult{
			err: fmt.Errorf("unsupported format: %s", format),
		}
	}
}

// Compresión JPEG con paralelismo
func compressJPEGParallel(buf *bytes.Buffer, img image.Image, numWorkers int) error {
	return jpeg.Encode(buf, img, &jpeg.Options{Quality: 60})
}

// Compresión PNG con paralelismo
func compressPNGParallel(buf *bytes.Buffer, img image.Image, numWorkers int) error {
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	return encoder.Encode(buf, img)
}
