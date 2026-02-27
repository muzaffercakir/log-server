package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	yzip "github.com/yeka/zip"
)

// getNextZipName verilen dizinde dosya adı çakışması varsa _2, _3, ... suffix ekler.
// Örn: full_13_02_2026.zip varsa → full_13_02_2026_2.zip, o da varsa → full_13_02_2026_3.zip
func getNextZipName(dir, baseName string) string {
	// İlk deneme: baseName.zip
	zipPath := filepath.Join(dir, baseName+".zip")
	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		return zipPath
	}

	// Çakışma var, _2, _3, ... dene
	for i := 2; ; i++ {
		zipPath = filepath.Join(dir, fmt.Sprintf("%s_%d.zip", baseName, i))
		if _, err := os.Stat(zipPath); os.IsNotExist(err) {
			return zipPath
		}
	}
}

// mergeAndZipFiles belirli JSON dosyalarını birleştirip şifreli zip olarak kaydeder.
// matchingFiles: dosya adları listesi (sadece ad, yol değil)
// homeIdPath: JSON dosyalarının bulunduğu klasör yolu
// backupDir: backup ana dizini (ör: ./backups)
// homeIdDir: home_id klasör adı (ör: home_id_xxx)
// today: tarih string'i (DD_MM_YYYY)
// password: zip şifresi
// Dönen değerler: zipPath, silinen dosya sayısı, hata
func mergeAndZipFiles(matchingFiles []string, homeIdPath, backupDir, homeIdDir, today, password string) (string, int, error) {
	// Tüm JSON dosyalarını oku ve birleştir
	var allLogs []json.RawMessage

	for _, fileName := range matchingFiles {
		filePath := filepath.Join(homeIdPath, fileName)
		logs, err := readJSONLogs(filePath)
		if err != nil {
			continue
		}
		allLogs = append(allLogs, logs...)
	}

	if len(allLogs) == 0 {
		return "", 0, nil
	}

	// NDJSON formatında birleştir (her satır bir JSON objesi)
	var builder strings.Builder
	for i, log := range allLogs {
		builder.Write(log)
		if i < len(allLogs)-1 {
			builder.WriteByte('\n')
		}
	}
	mergedJSON := []byte(builder.String())

	// Hedef backup dizini: backups/home_id_xxx/
	targetBackupDir := filepath.Join(backupDir, homeIdDir)
	if err := os.MkdirAll(targetBackupDir, 0755); err != nil {
		return "", 0, fmt.Errorf("backup dizini oluşturulamadı: %w", err)
	}

	baseName := fmt.Sprintf("%s_%s_all_event_log", homeIdDir, today)
	zipFilePath := getNextZipName(targetBackupDir, baseName)

	// JSON dosya adı (zip içindeki entry adı)
	jsonEntryName := strings.TrimSuffix(filepath.Base(zipFilePath), ".zip") + ".json"

	// Geçici dosyaya yaz
	tempDir := os.TempDir()
	tempJSONPath := filepath.Join(tempDir, jsonEntryName)
	if err := os.WriteFile(tempJSONPath, mergedJSON, 0644); err != nil {
		return "", 0, fmt.Errorf("geçici JSON dosyası yazılamadı: %w", err)
	}
	defer os.Remove(tempJSONPath)

	// Şifreli zip oluştur
	if err := zipSingleFile(tempJSONPath, jsonEntryName, zipFilePath, password); err != nil {
		return "", 0, fmt.Errorf("zip oluşturma hatası: %w", err)
	}

	// Orijinal JSON dosyalarını sil
	deletedCount := 0
	for _, fileName := range matchingFiles {
		filePath := filepath.Join(homeIdPath, fileName)
		if err := os.Remove(filePath); err == nil {
			deletedCount++
		}
	}

	return zipFilePath, deletedCount, nil
}

// readJSONLogs bir JSON log dosyasını okur.
// Dosya NDJSON (her satır bir JSON objesi) veya JSON array olabilir.
func readJSONLogs(filePath string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	// Önce JSON array olarak dene
	var arrayLogs []json.RawMessage
	if err := json.Unmarshal(data, &arrayLogs); err == nil {
		return arrayLogs, nil
	}

	// NDJSON (newline-delimited JSON) olarak dene
	var logs []json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			break
		}
		logs = append(logs, raw)
	}

	return logs, nil
}

// zipSingleFile tek bir dosyayı şifreli zip olarak oluşturur.
func zipSingleFile(sourceFilePath, entryName, zipPath, password string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("zip dosyası oluşturulamadı: %w", err)
	}
	defer zipFile.Close()

	archive := yzip.NewWriter(zipFile)
	defer archive.Close()

	var writer io.Writer

	if password != "" {
		writer, err = archive.Encrypt(entryName, password, yzip.AES256Encryption)
		if err != nil {
			return fmt.Errorf("zip encrypt hatası: %w", err)
		}
	} else {
		header := &yzip.FileHeader{
			Name:   entryName,
			Method: yzip.Deflate,
		}
		writer, err = archive.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("zip header oluşturma hatası: %w", err)
		}
	}

	sourceFile, err := os.Open(sourceFilePath)
	if err != nil {
		return fmt.Errorf("kaynak dosya açılamadı: %w", err)
	}
	defer sourceFile.Close()

	if _, err := io.Copy(writer, sourceFile); err != nil {
		return fmt.Errorf("zip yazma hatası: %w", err)
	}

	return nil
}
