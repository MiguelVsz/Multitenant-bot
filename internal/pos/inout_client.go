package pos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
	// Construimos la URL con el BusinessID
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
		fmt.Printf("[ERROR] Error de red: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] Respuesta API PointSales: %d %s\n", resp.StatusCode, resp.Status)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error en API: %s", resp.Status)
	}

	// TRUCO: Decodificamos como una lista directa []InOutStore
	var storesData []InOutStore
	if err := json.NewDecoder(resp.Body).Decode(&storesData); err != nil {
		fmt.Printf("[ERROR] Error decodificando JSON: %v\n", err)
		return nil, err
	}

	// Extraemos los nombres para el bot
	var names []string
	for _, s := range storesData {
		names = append(names, s.Name)
	}

	fmt.Printf("[DEBUG] Tiendas cargadas: %d\n", len(names))
	return names, nil
}

// 5. Función para actualizar usuario (Patch)
func (c *InOutClient) UpdateUser(userID string, data map[string]interface{}) error {
	url := fmt.Sprintf("%s/users/%s", c.BaseURL, userID)
	fmt.Printf("[DEBUG] Intentando PATCH en: %s con datos: %v\n", url, data)

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
		fmt.Printf("[ERROR] Fallo en PATCH: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] Resultado PATCH: %d %s\n", resp.StatusCode, resp.Status)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("error al actualizar: %s", resp.Status)
	}

	return nil
}
func (c *InOutClient) GetUserIDByPhone(phone string) (string, error) {
	// Consultamos la lista de usuarios filtrando por teléfono
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

	// Estructura temporal para leer la respuesta de la lista de usuarios
	var result struct {
		Data []struct {
			ID string `json:"rid"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Data) > 0 {
		return result.Data[0].ID, nil // Devolvemos el RID real
	}

	return "", fmt.Errorf("usuario no encontrado")
}
