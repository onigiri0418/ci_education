package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// setupRouter configures routes and middleware.
func setupRouter() *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	r.GET("/hello", func(c *gin.Context) {
		name := c.Query("name")
		if name == "" {
			name = "world"
		}
		c.JSON(http.StatusOK, gin.H{"message": "hello " + name})
	})

	r.GET("/pokemon/:name", func(c *gin.Context) {
		name := c.Param("name")
		baseURL := getenv("POKEAPI_BASE_URL", "https://pokeapi.co/api/v2")
		url := fmt.Sprintf("%s/pokemon/%s", baseURL, name)

		resp, err := http.Get(url)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch Pokemon"})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			c.JSON(resp.StatusCode, gin.H{"error": "pokemon not found"})
			return
		}

		var data struct {
			Name           string `json:"name"`
			Height         int    `json:"height"`
			Weight         int    `json:"weight"`
			BaseExperience int    `json:"base_experience"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response"})
			return
		}

		c.JSON(http.StatusOK, data)
	})

	return r
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	r := setupRouter()
	port := getenv("PORT", "8080")
	// Ignoring the error here keeps the example minimal; Gin logs it.
	_ = r.Run(":" + port)
}
