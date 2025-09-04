package main

import (
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

