package pos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url" // <--- ESTE ES EL QUE FALTABA PARA EL ESCAPED_ID
	"os"
	"time"
)

// 1. Molde para recibir los datos de la API (Lista de tiendas)
type InOutStore struct {
	Name string `json:"name"`
	ID   string `json:"rid"`
}

// 2. Estructura del Cliente
type InOutClient struct {
	BaseURL    string
	Token      string
	BusinessID string
	HTTP       *http.Client
}

// 3. Constructor del Cliente
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

// 4. Función para obtener las tiendas (Get)
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

// 5. Función para buscar el ID real del usuario por su teléfono
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
		return result.Data[0].ID, nil // El RID real que vimos en Postman
	}

	return "", fmt.Errorf("usuario no encontrado")
}

// 6. Función para actualizar usuario (Patch) con ID Escapado
func (c *InOutClient) UpdateUser(userID string, data map[string]interface{}) error {
	// IMPORTANTE: Convierte símbolos como # o : para que la URL no se rompa
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
