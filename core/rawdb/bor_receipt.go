package rawdb

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

var (
	// bor receipt key
	borReceiptKey = types.BorReceiptKey

	// bor derived tx hash
	getDerivedBorTxHash = types.GetDerivedBorTxHash

	// borTxLookupPrefix + hash -> transaction/receipt lookup metadata
	borTxLookupPrefix = []byte(borTxLookupPrefixStr)
)

const (
	borTxLookupPrefixStr = "matic-bor-tx-lookup-"

	// freezerBorReceiptTable indicates the name of the freezer bor receipts table.
	freezerBorReceiptTable = "matic-bor-receipts"
)

// borTxLookupKey = borTxLookupPrefix + bor tx hash
func borTxLookupKey(hash common.Hash) []byte {
	return append(borTxLookupPrefix, hash.Bytes()...)
}

// HasBorReceipt verifies the existence of all block receipt belonging
// to a block.
func HasBorReceipt(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}

	if has, err := db.Has(borReceiptKey(number, hash)); !has || err != nil {
		return false
	}

	return true
}

// ReadBorReceiptRLP retrieves the block receipt belonging to a block in RLP encoding.
func ReadBorReceiptRLP(db ethdb.Reader, hash common.Hash, number uint64) rlp.RawValue {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerBorReceiptTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	// Then try to look up the data in leveldb.
	data, _ = db.Get(borReceiptKey(number, hash))
	if len(data) > 0 {
		return data
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerBorReceiptTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	return nil // Can't find the data anywhere.
}

// ReadRawBorReceipt retrieves the block receipt belonging to a block.
// The receipt metadata fields are not guaranteed to be populated, so they
// should not be used. Use ReadBorReceipt instead if the metadata is needed.
func ReadRawBorReceipt(db ethdb.Reader, hash common.Hash, number uint64) *types.Receipt {
	// Retrieve the flattened receipt slice
	data := ReadBorReceiptRLP(db, hash, number)
	if data == nil || len(data) == 0 {
		return nil
	}

	// Convert the receipts from their storage form to their internal representation
	// [mys] additional receipts logic to handle incorrect ones
	var storageReceipt types.ReceiptForStorage
	if err := rlp.DecodeBytes(data, &storageReceipt); err != nil {
		storageReceipts := []*types.ReceiptForStorage{}
		if err := rlp.DecodeBytes(data, &storageReceipts); err != nil {
			log.Error("Invalid bor receipt array RLP", "number", number, "hash", hash, "err", err)
			return nil
		}
		if nReceipts := len(storageReceipts); nReceipts != 1 {
			log.Error("Invalid bor receipt array RLP length", "number", number, "hash", hash, "nReceipts", nReceipts)
			return nil
		}
		return (*types.Receipt)(storageReceipts[0])
	}

	return (*types.Receipt)(&storageReceipt)
}

// ReadBorReceipt retrieves all the bor block receipts belonging to a block, including
// its correspoinding metadata fields. If it is unable to populate these metadata
// fields then nil is returned.
func ReadBorReceipt(db ethdb.Reader, hash common.Hash, number uint64) *types.Receipt {
	// We're deriving many fields from the block body, retrieve beside the receipt
	borReceipt := ReadRawBorReceipt(db, hash, number)
	if borReceipt == nil {
		return nil
	}

	// We're deriving many fields from the block body, retrieve beside the receipt
	receipts := ReadRawReceipts(db, hash, number)
	if receipts == nil {
		return nil
	}

	body := ReadBody(db, hash, number)
	if body == nil {
		log.Error("Missing body but have bor receipt", "hash", hash, "number", number)
		return nil
	}

	if err := types.DeriveFieldsForBorReceipt(borReceipt, hash, number, receipts); err != nil {
		log.Error("Failed to derive bor receipt fields", "hash", hash, "number", number, "err", err)
		return nil
	}
	return borReceipt
}

// WriteBorReceipt stores all the bor receipt belonging to a block.
func WriteBorReceipt(db ethdb.KeyValueWriter, hash common.Hash, number uint64, borReceipt *types.ReceiptForStorage) {
	// Convert the bor receipt into their storage form and serialize them
	bytes, err := rlp.EncodeToBytes(borReceipt)
	if err != nil {
		log.Crit("Failed to encode bor receipt", "err", err)
	}

	// Store the flattened receipt slice
	if err := db.Put(borReceiptKey(number, hash), bytes); err != nil {
		log.Crit("Failed to store bor receipt", "err", err)
	}
}

// DeleteBorReceipt removes receipt data associated with a block hash.
func DeleteBorReceipt(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	key := borReceiptKey(number, hash)

	if err := db.Delete(key); err != nil {
		log.Crit("Failed to delete bor receipt", "err", err)
	}
}

// ReadBorTransactionWithBlockHash retrieves a specific bor (fake) transaction by tx hash and block hash, along with
// its added positional metadata.
func ReadBorTransactionWithBlockHash(db ethdb.Reader, txHash common.Hash, blockHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64) {
	blockNumber := ReadBorTxLookupEntry(db, txHash)
	if blockNumber == nil {
		return nil, common.Hash{}, 0, 0
	}

	body := ReadBody(db, blockHash, *blockNumber)
	if body == nil {
		log.Error("Transaction referenced missing", "number", blockNumber, "hash", blockHash)
		return nil, common.Hash{}, 0, 0
	}

	// fetch receipt and return it
	return types.NewBorTransaction(), blockHash, *blockNumber, uint64(len(body.Transactions))
}

// ReadBorTransaction retrieves a specific bor (fake) transaction by hash, along with
// its added positional metadata.
func ReadBorTransaction(db ethdb.Reader, hash common.Hash) (*types.Transaction, common.Hash, uint64, uint64) {
	blockNumber := ReadBorTxLookupEntry(db, hash)
	if blockNumber == nil {
		return nil, common.Hash{}, 0, 0
	}

	blockHash := ReadCanonicalHash(db, *blockNumber)
	if blockHash == (common.Hash{}) {
		return nil, common.Hash{}, 0, 0
	}

	body := ReadBody(db, blockHash, *blockNumber)
	if body == nil {
		log.Error("Transaction referenced missing", "number", blockNumber, "hash", blockHash)
		return nil, common.Hash{}, 0, 0
	}

	// fetch receipt and return it
	return types.NewBorTransaction(), blockHash, *blockNumber, uint64(len(body.Transactions))
}

//
// Indexes for reverse lookup
//

// ReadBorTxLookupEntry retrieves the positional metadata associated with a transaction
// hash to allow retrieving the bor transaction or bor receipt using tx hash.
func ReadBorTxLookupEntry(db ethdb.Reader, txHash common.Hash) *uint64 {
	data, _ := db.Get(borTxLookupKey(txHash))
	if len(data) == 0 {
		return nil
	}

	number := new(big.Int).SetBytes(data).Uint64()
	return &number
}

// WriteBorTxLookupEntry stores a positional metadata for bor transaction using block hash and block number
func WriteBorTxLookupEntry(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	txHash := types.GetDerivedBorTxHash(borReceiptKey(number, hash))
	if err := db.Put(borTxLookupKey(txHash), big.NewInt(0).SetUint64(number).Bytes()); err != nil {
		log.Crit("Failed to store bor transaction lookup entry", "err", err)
	}
}

// DeleteBorTxLookupEntry removes bor transaction data associated with block hash and block number
func DeleteBorTxLookupEntry(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	txHash := types.GetDerivedBorTxHash(borReceiptKey(number, hash))
	DeleteBorTxLookupEntryByTxHash(db, txHash)
}

// DeleteBorTxLookupEntryByTxHash removes bor transaction data associated with a bor tx hash.
func DeleteBorTxLookupEntryByTxHash(db ethdb.KeyValueWriter, txHash common.Hash) {
	if err := db.Delete(borTxLookupKey(txHash)); err != nil {
		log.Crit("Failed to delete bor transaction lookup entry", "err", err)
	}
}
