package game

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// EconomyDB manages persistent player accounts and transactional history in SQLite.
type EconomyDB struct {
	db *sql.DB
	mu sync.RWMutex
}

type AccountRecord struct {
	AccountID string  `json:"accountId"`
	Name      string  `json:"name"`
	Balance   float64 `json:"balance"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

type TransactionRecord struct {
	ID               int64   `json:"id"`
	TxType           string  `json:"txType"`
	FromAccount      string  `json:"fromAccount"`
	ToAccount        string  `json:"toAccount"`
	Amount           float64 `json:"amount"`
	FromBalanceAfter float64 `json:"fromBalanceAfter"`
	ToBalanceAfter   float64 `json:"toBalanceAfter"`
	Reason           string  `json:"reason"`
	CreatedAt        int64   `json:"createdAt"`
}

const defaultInitialBalance = 1000.0

func initEconomyDB(dataDir string) (*EconomyDB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "economy.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open economy db: %w", err)
	}

	// Schema initialization
	schema := `
	CREATE TABLE IF NOT EXISTS accounts (
		account_id TEXT PRIMARY KEY,
		player_name TEXT NOT NULL,
		balance REAL NOT NULL DEFAULT 1000.0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_accounts_name ON accounts(player_name COLLATE NOCASE);

	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tx_type TEXT NOT NULL,
		from_account TEXT,
		to_account TEXT,
		amount REAL NOT NULL,
		from_balance_after REAL,
		to_balance_after REAL,
		reason TEXT,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tx_from ON transactions(from_account);
	CREATE INDEX IF NOT EXISTS idx_tx_to ON transactions(to_account);

	CREATE TABLE IF NOT EXISTS player_nametags (
		account_id TEXT PRIMARY KEY,
		player_name TEXT NOT NULL,
		prefix TEXT NOT NULL DEFAULT '',
		suffix TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL DEFAULT '',
		name_color TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		title_color TEXT NOT NULL DEFAULT '',
		title_icon TEXT NOT NULL DEFAULT '',
		title_animation TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &EconomyDB{db: db}, nil
}

func (e *EconomyDB) Close() error {
	if e == nil || e.db == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.db.Close()
}

// EnsureAccount creates an account with default 1000 balance if it doesn't exist,
// or updates the player's last known name.
func (e *EconomyDB) EnsureAccount(accountID, name string) (*AccountRecord, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("economy db not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	var acc AccountRecord
	err := e.db.QueryRow(`SELECT account_id, player_name, balance, created_at, updated_at FROM accounts WHERE account_id = ?`, accountID).
		Scan(&acc.AccountID, &acc.Name, &acc.Balance, &acc.CreatedAt, &acc.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Create new account with starting funds
		acc = AccountRecord{
			AccountID: accountID,
			Name:      name,
			Balance:   defaultInitialBalance,
			CreatedAt: now,
			UpdatedAt: now,
		}
		_, err = e.db.Exec(`INSERT INTO accounts (account_id, player_name, balance, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			acc.AccountID, acc.Name, acc.Balance, acc.CreatedAt, acc.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("create account: %w", err)
		}
		// Log welcome grant
		_, _ = e.db.Exec(`INSERT INTO transactions (tx_type, from_account, to_account, amount, from_balance_after, to_balance_after, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"init_grant", "", accountID, defaultInitialBalance, 0, defaultInitialBalance, "新玩家初始注册金币", now)
		return &acc, nil
	} else if err != nil {
		return nil, fmt.Errorf("query account: %w", err)
	}

	// Update name if changed
	if name != "" && acc.Name != name {
		acc.Name = name
		acc.UpdatedAt = now
		_, _ = e.db.Exec(`UPDATE accounts SET player_name = ?, updated_at = ? WHERE account_id = ?`, name, now, accountID)
	}
	return &acc, nil
}

// GetAccountByID retrieves account by accountID.
func (e *EconomyDB) GetAccountByID(accountID string) (*AccountRecord, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("economy db not initialized")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	var acc AccountRecord
	err := e.db.QueryRow(`SELECT account_id, player_name, balance, created_at, updated_at FROM accounts WHERE account_id = ?`, accountID).
		Scan(&acc.AccountID, &acc.Name, &acc.Balance, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// FindAccountByName finds an account by exact or case-insensitive player name.
func (e *EconomyDB) FindAccountByName(name string) (*AccountRecord, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("economy db not initialized")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	var acc AccountRecord
	err := e.db.QueryRow(`SELECT account_id, player_name, balance, created_at, updated_at FROM accounts WHERE player_name = ? COLLATE NOCASE ORDER BY updated_at DESC LIMIT 1`, name).
		Scan(&acc.AccountID, &acc.Name, &acc.Balance, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// GetBalance returns the balance of accountID.
func (e *EconomyDB) GetBalance(accountID string) (float64, error) {
	acc, err := e.GetAccountByID(accountID)
	if err != nil {
		return 0, err
	}
	return acc.Balance, nil
}

// Deposit adds money to an account.
func (e *EconomyDB) Deposit(accountID, name string, amount float64, reason string) (float64, error) {
	if amount <= 0 {
		return 0, errors.New("deposit amount must be positive")
	}
	if e == nil || e.db == nil {
		return 0, errors.New("economy db not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	tx, err := e.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var balance float64
	err = tx.QueryRow(`SELECT balance FROM accounts WHERE account_id = ?`, accountID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		balance = defaultInitialBalance
		_, err = tx.Exec(`INSERT INTO accounts (account_id, player_name, balance, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			accountID, name, balance, now, now)
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	newBalance := balance + amount
	_, err = tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE account_id = ?`, newBalance, now, accountID)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(`INSERT INTO transactions (tx_type, from_account, to_account, amount, from_balance_after, to_balance_after, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"deposit", "", accountID, amount, 0, newBalance, reason, now)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newBalance, nil
}

// Withdraw subtracts money from an account if sufficient balance exists.
func (e *EconomyDB) Withdraw(accountID string, amount float64, reason string) (float64, error) {
	if amount <= 0 {
		return 0, errors.New("withdraw amount must be positive")
	}
	if e == nil || e.db == nil {
		return 0, errors.New("economy db not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	tx, err := e.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var balance float64
	err = tx.QueryRow(`SELECT balance FROM accounts WHERE account_id = ?`, accountID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("account not found: %w", err)
	}
	if balance < amount {
		return balance, fmt.Errorf("余额不足：当前余额 $%.2f，需要 $%.2f", balance, amount)
	}

	newBalance := balance - amount
	_, err = tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE account_id = ?`, newBalance, now, accountID)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(`INSERT INTO transactions (tx_type, from_account, to_account, amount, from_balance_after, to_balance_after, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"withdraw", accountID, "", amount, newBalance, 0, reason, now)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newBalance, nil
}

// SetBalance forces a player's balance to an exact amount.
func (e *EconomyDB) SetBalance(accountID, name string, amount float64, reason string) (float64, error) {
	if amount < 0 {
		return 0, errors.New("balance cannot be negative")
	}
	if e == nil || e.db == nil {
		return 0, errors.New("economy db not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	tx, err := e.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var oldBalance float64
	err = tx.QueryRow(`SELECT balance FROM accounts WHERE account_id = ?`, accountID).Scan(&oldBalance)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO accounts (account_id, player_name, balance, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			accountID, name, amount, now, now)
	} else if err == nil {
		_, err = tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE account_id = ?`, amount, now, accountID)
	}
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(`INSERT INTO transactions (tx_type, from_account, to_account, amount, from_balance_after, to_balance_after, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"admin_set", "", accountID, amount, oldBalance, amount, reason, now)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return amount, nil
}

// Transfer performs an atomic transfer between two accounts within a single SQL transaction.
func (e *EconomyDB) Transfer(fromID, toID, fromName, toName string, amount float64, reason string) (float64, float64, error) {
	if amount <= 0 {
		return 0, 0, errors.New("转账金额必须大于 0")
	}
	if fromID == toID {
		return 0, 0, errors.New("不能向自己转账")
	}
	if e == nil || e.db == nil {
		return 0, 0, errors.New("economy db not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	tx, err := e.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// 1. Check sender balance
	var fromBal float64
	err = tx.QueryRow(`SELECT balance FROM accounts WHERE account_id = ?`, fromID).Scan(&fromBal)
	if err != nil {
		return 0, 0, fmt.Errorf("付款方账户不存在: %w", err)
	}
	if fromBal < amount {
		return fromBal, 0, fmt.Errorf("余额不足：当前余额 $%.2f，需要 $%.2f", fromBal, amount)
	}

	// 2. Ensure recipient account exists
	var toBal float64
	err = tx.QueryRow(`SELECT balance FROM accounts WHERE account_id = ?`, toID).Scan(&toBal)
	if errors.Is(err, sql.ErrNoRows) {
		toBal = defaultInitialBalance
		_, err = tx.Exec(`INSERT INTO accounts (account_id, player_name, balance, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			toID, toName, toBal, now, now)
		if err != nil {
			return 0, 0, fmt.Errorf("创建收款方账户失败: %w", err)
		}
	} else if err != nil {
		return 0, 0, fmt.Errorf("查询收款方账户失败: %w", err)
	}

	newFromBal := fromBal - amount
	newToBal := toBal + amount

	// 3. Update both balances atomically
	_, err = tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE account_id = ?`, newFromBal, now, fromID)
	if err != nil {
		return 0, 0, err
	}
	_, err = tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE account_id = ?`, newToBal, now, toID)
	if err != nil {
		return 0, 0, err
	}

	// 4. Log transaction
	_, err = tx.Exec(`INSERT INTO transactions (tx_type, from_account, to_account, amount, from_balance_after, to_balance_after, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"transfer", fromID, toID, amount, newFromBal, newToBal, reason, now)
	if err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return newFromBal, newToBal, nil
}

// GetNametag retrieves the persistent nametag info for an account.
func (e *EconomyDB) GetNametag(accountID string) (*NametagInfo, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("database not initialized")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	var info NametagInfo
	var playerName string
	var updatedAt int64
	row := e.db.QueryRow(`SELECT prefix, suffix, display_name, name_color, title, title_color, title_icon, title_animation, player_name, updated_at FROM player_nametags WHERE account_id = ?`, accountID)
	err := row.Scan(&info.Prefix, &info.Suffix, &info.DisplayName, &info.NameColor, &info.Title, &info.TitleColor, &info.TitleIcon, &info.TitleAnimation, &playerName, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// SaveNametag inserts or updates the persistent nametag info for an account.
func (e *EconomyDB) SaveNametag(accountID, playerName string, info *NametagInfo) error {
	if e == nil || e.db == nil {
		return errors.New("database not initialized")
	}
	if info == nil {
		return e.ClearNametag(accountID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()
	_, err := e.db.Exec(`
		INSERT INTO player_nametags (account_id, player_name, prefix, suffix, display_name, name_color, title, title_color, title_icon, title_animation, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			player_name = excluded.player_name,
			prefix = excluded.prefix,
			suffix = excluded.suffix,
			display_name = excluded.display_name,
			name_color = excluded.name_color,
			title = excluded.title,
			title_color = excluded.title_color,
			title_icon = excluded.title_icon,
			title_animation = excluded.title_animation,
			updated_at = excluded.updated_at
	`, accountID, playerName, info.Prefix, info.Suffix, info.DisplayName, info.NameColor, info.Title, info.TitleColor, info.TitleIcon, info.TitleAnimation, now)
	return err
}

// ClearNametag deletes the persistent nametag info for an account.
func (e *EconomyDB) ClearNametag(accountID string) error {
	if e == nil || e.db == nil {
		return errors.New("database not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	_, err := e.db.Exec(`DELETE FROM player_nametags WHERE account_id = ?`, accountID)
	return err
}
