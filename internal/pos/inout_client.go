package pos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

type InOutStore struct {
	Name string `json:"name"`
	ID   string `json:"rid"`
}

type InOutProduct struct {
	RID  string `json:"rid"`
	Name string `json:"name"`
	Code string `json:"code"`
}

type InOutClient struct {
	BaseURL    string
	Token      string
	BusinessID string
	HTTP       *http.Client
}

func NewInOutClient() *InOutClient {
	return &InOutClient{
		BaseURL:    os.Getenv("INOUT_BASE_URL"),
		Token:      os.Getenv("INOUT_TOKEN"),
		BusinessID: os.Getenv("INOUT_BUSINESS_ID"),
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *InOutClient) GetPointSales() ([]string, error) {
	url := fmt.Sprintf("%s/point-sales?business=%s", c.BaseURL, c.BusinessID)
	fmt.Printf("[DEBUG] Consultando tiendas en: %s\n", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error en API: %s", resp.Status)
	}

	var storesData []InOutStore
	if err := json.NewDecoder(resp.Body).Decode(&storesData); err != nil {
		return nil, err
	}

	var names []string
	for _, s := range storesData {
		names = append(names, s.Name)
	}
	return names, nil
}

func (c *InOutClient) GetProducts() ([]InOutProduct, error) {
	url := fmt.Sprintf("%s/products", c.BaseURL)

	body := map[string]interface{}{
		"business":          c.BusinessID,
		"skipEmptyProducts": "true",
	}
	jsonData, _ := json.Marshal(body)

	req, err := http.NewRequest("GET", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var products []InOutProduct
	if err := json.NewDecoder(resp.Body).Decode(&products); err != nil {
		return nil, err
	}
	return products, nil
}

func (c *InOutClient) GetUserIDByPhone(phone string) (string, error) {
	url := fmt.Sprintf("%s/users?business=%s&search=%s", c.BaseURL, c.BusinessID, phone)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"rid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Data) > 0 {
		return result.Data[0].ID, nil
	}
	return "", fmt.Errorf("usuario no encontrado")
}

func (c *InOutClient) UpdateUser(userID string, data map[string]interface{}) error {
	escapedID := url.PathEscape(userID)
	url := fmt.Sprintf("%s/users/%s?business=%s", c.BaseURL, escapedID, c.BusinessID)
	fmt.Printf("[DEBUG] Intentando PATCH en: %s\n", url)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] Resultado PATCH: %d %s\n", resp.StatusCode, resp.Status)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("error al actualizar: %s", resp.Status)
	}
	return nil
}
