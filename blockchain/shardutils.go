package blockchain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/ninjadotorg/constant/common"
	"github.com/ninjadotorg/constant/common/base58"
	"github.com/ninjadotorg/constant/database"
	"github.com/ninjadotorg/constant/metadata"
	"github.com/ninjadotorg/constant/privacy"
	"github.com/ninjadotorg/constant/transaction"
)

//=======================================BEGIN SHARD BLOCK UTIL
func GetAssignInstructionFromBeaconBlock(beaconBlocks []*BeaconBlock, shardID byte) [][]string {
	assignInstruction := [][]string{}
	for _, beaconBlock := range beaconBlocks {
		for _, l := range beaconBlock.Body.Instructions {
			if l[0] == "assign" && l[2] == "shard" {
				if strings.Compare(l[3], strconv.Itoa(int(shardID))) == 0 {
					assignInstruction = append(assignInstruction, l)
				}
			}
		}
	}
	return assignInstruction
}

func FetchBeaconBlockFromHeight(db database.DatabaseInterface, from uint64, to uint64) ([]*BeaconBlock, error) {
	beaconBlocks := []*BeaconBlock{}
	for i := from; i <= to; i++ {
		hash, err := db.GetBeaconBlockHashByIndex(i)
		if err != nil {
			return beaconBlocks, err
		}
		beaconBlockByte, err := db.FetchBeaconBlock(hash)
		if err != nil {
			return beaconBlocks, err
		}
		beaconBlock := BeaconBlock{}
		err = json.Unmarshal(beaconBlockByte, &beaconBlock)
		if err != nil {
			return beaconBlocks, NewBlockChainError(UnmashallJsonBlockError, err)
		}
		beaconBlocks = append(beaconBlocks, &beaconBlock)
	}
	return beaconBlocks, nil
}

func CreateCrossShardByteArray(txList []metadata.Transaction, fromShardID byte) []byte {
	crossIDs := []byte{}
	byteMap := make([]byte, common.MAX_SHARD_NUMBER)
	for _, tx := range txList {
		switch tx.GetType() {
		case common.TxNormalType, common.TxSalaryType:
			{
				if tx.GetProof() != nil {
					for _, outCoin := range tx.GetProof().OutputCoins {
						lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
						shardID := common.GetShardIDFromLastByte(lastByte)
						byteMap[common.GetShardIDFromLastByte(shardID)] = 1
					}
				}
			}
		case common.TxCustomTokenType:
			{
				customTokenTx := tx.(*transaction.TxCustomToken)
				for _, out := range customTokenTx.TxTokenData.Vouts {
					lastByte := out.PaymentAddress.Pk[len(out.PaymentAddress.Pk)-1]
					shardID := common.GetShardIDFromLastByte(lastByte)
					byteMap[common.GetShardIDFromLastByte(shardID)] = 1
				}
			}
		case common.TxCustomTokenPrivacyType:
			{
				customTokenTx := tx.(*transaction.TxCustomTokenPrivacy)
				if customTokenTx.TxTokenPrivacyData.TxNormal.GetProof() != nil {
					for _, outCoin := range customTokenTx.TxTokenPrivacyData.TxNormal.GetProof().OutputCoins {
						lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
						shardID := common.GetShardIDFromLastByte(lastByte)
						byteMap[common.GetShardIDFromLastByte(shardID)] = 1
					}
				}
			}
		}
	}

	for k := range byteMap {
		if byteMap[k] == 1 && k != int(fromShardID) {
			crossIDs = append(crossIDs, byte(k))
		}
	}

	return crossIDs
}

/*
	Create Swap Action
	Return param:
	#1: swap instruction
	#2: new pending validator list after swapped
	#3: new committees after swapped
	#4: error
*/
func CreateSwapAction(pendingValidator []string, commitees []string, committeeSize int, shardID byte) ([]string, []string, []string, error) {
	fmt.Println("Shard Producer/Create Swap Action: pendingValidator", pendingValidator)
	fmt.Println("Shard Producer/Create Swap Action: commitees", commitees)
	newPendingValidator, newShardCommittees, shardSwapedCommittees, shardNewCommittees, err := SwapValidator(pendingValidator, commitees, committeeSize, common.OFFSET)
	if err != nil {
		return nil, nil, nil, err
	}
	swapInstruction := []string{"swap", strings.Join(shardNewCommittees, ","), strings.Join(shardSwapedCommittees, ","), "shard", strconv.Itoa(int(shardID))}
	return swapInstruction, newPendingValidator, newShardCommittees, nil
}

/*
	Action Generate From Transaction:
	- Stake
	- Stable param: set, del,...
*/
func CreateShardInstructionsFromTransactionAndIns(
	transactions []metadata.Transaction,
	bc *BlockChain,
	shardID byte,
	producerAddress *privacy.PaymentAddress,
	shardBlockHeight uint64,
	beaconBlocks []*BeaconBlock,
) (instructions [][]string) {
	// Generate stake action
	stakeShardPubKey := []string{}
	stakeBeaconPubKey := []string{}
	instructions = buildStabilityActions(transactions, bc, shardID, producerAddress, shardBlockHeight, beaconBlocks)

	for _, tx := range transactions {
		switch tx.GetMetadataType() {
		case metadata.ShardStakingMeta:
			pk := tx.GetProof().InputCoins[0].CoinDetails.PublicKey.Compress()
			pkb58 := base58.Base58Check{}.Encode(pk, common.ZeroByte)
			stakeShardPubKey = append(stakeShardPubKey, pkb58)
		case metadata.BeaconStakingMeta:
			pk := tx.GetProof().InputCoins[0].CoinDetails.PublicKey.Compress()
			pkb58 := base58.Base58Check{}.Encode(pk, common.ZeroByte)
			stakeBeaconPubKey = append(stakeBeaconPubKey, pkb58)
			//TODO: stable param 0xsancurasolus
			// case metadata.BuyFromGOVRequestMeta:
		}
	}

	if !reflect.DeepEqual(stakeShardPubKey, []string{}) {
		instruction := []string{StakeAction, strings.Join(stakeShardPubKey, ","), "shard"}
		instructions = append(instructions, instruction)
	}
	if !reflect.DeepEqual(stakeBeaconPubKey, []string{}) {
		instruction := []string{StakeAction, strings.Join(stakeBeaconPubKey, ","), "beacon"}
		instructions = append(instructions, instruction)
	}

	return instructions
}

//=======================================END SHARD BLOCK UTIL
//=======================================BEGIN CROSS SHARD UTIL
/*
	Return value #1: outputcoin hash
	Return value #2: merkle data created from outputcoin hash
*/
func CreateShardTxRoot(txList []metadata.Transaction) ([]common.Hash, []common.Hash) {
	//calculate output coin hash for each shard
	crossShardDataHash := getCrossShardDataHash(txList)
	// calculate merkel path for a shardID
	// step 1: calculate merkle data : [1, 2, 3, 4, 12, 34, 1234]
	/*
			   	1234=hash(12,34)
			   /			  \
		  12=hash(1,2)	 34=hash(3,4)
			 / \	 		 / \
			1	2			3	4
	*/
	merkleData := crossShardDataHash
	cursor := 0
	for {
		v1 := merkleData[cursor]
		v2 := merkleData[cursor+1]
		merkleData = append(merkleData, common.HashH(append(v1.GetBytes(), v2.GetBytes()...)))
		cursor += 2
		if cursor >= len(merkleData)-1 {
			break
		}
	}
	return crossShardDataHash, merkleData
}

//Receive tx list from shard block body, produce merkle path of UTXO CrossShard List from specific shardID
func GetMerklePathCrossShard(txList []metadata.Transaction, shardID byte) (merklePathShard []common.Hash, merkleShardRoot common.Hash) {
	crossShardDataHash, merkleData := CreateShardTxRoot(txList)
	// step 2: get merkle path
	cursor := 0
	lastCursor := 0
	sid := int(shardID)
	i := sid
	time := 0
	for {
		if cursor >= len(merkleData)-2 {
			break
		}
		if i%2 == 0 {
			merklePathShard = append(merklePathShard, merkleData[cursor+i+1])
		} else {
			merklePathShard = append(merklePathShard, merkleData[cursor+i-1])
		}
		i = i / 2

		if time == 0 {
			cursor += len(crossShardDataHash)
		} else {
			tmp := cursor
			cursor += (cursor - lastCursor) / 2
			lastCursor = tmp
		}
		time++
	}
	merkleShardRoot = merkleData[len(merkleData)-1]
	return merklePathShard, merkleShardRoot
}

//Receive a cross shard block and merkle path, verify whether the UTXO list is valid or not
/*
	Calculate Final Hash as Hash of:
		1. CrossOutputCoinFinalHash
		2. TxTokenDataVoutFinalHash
	These hashes will be calculated as comment in getCrossShardDataHash function
*/
func VerifyCrossShardBlockUTXO(block *CrossShardBlock, merklePathShard []common.Hash) bool {
	outCoins := block.CrossOutputCoin
	var outputCoinHash common.Hash
	var txTokenDataHash common.Hash
	outputCoinHash = calHashOutCoinCrossShard(outCoins)
	// if len(outCoins) != 0 {
	// 	for _, outCoin := range outCoins {
	// 		coin := &outCoin
	// 		tmpByte = append(tmpByte, coin.Bytes()...)
	// 	}
	// 	outputCoinHash = common.HashH(tmpByte)
	// } else {
	// 	outputCoinHash = common.HashH([]byte(""))
	// }

	txTokenDataList := block.CrossTxTokenData
	txTokenDataHash = calHashTxTokenDataHashList(txTokenDataList)
	// tmpByte = []byte{}
	// if len(txTokenDatas) != 0 {
	// 	for _, txTokenData := range txTokenDatas {
	// 		for _, out := range txTokenData.Vouts {
	// 			tmpByte = append(tmpByte, []byte(out.String())...)
	// 		}
	// 	}
	// 	txTokenDataHash = common.HashH(tmpByte)
	// } else {
	// 	txTokenDataHash = common.HashH([]byte(""))
	// }
	finalHash := common.HashH(append(outputCoinHash.GetBytes(), txTokenDataHash.GetBytes()...))
	return VerifyMerkleTree(finalHash, merklePathShard, block.Header.ShardTxRoot, block.ToShardID)
}

func VerifyMerkleTree(finalHash common.Hash, merklePath []common.Hash, merkleRoot common.Hash, receiverShardID byte) bool {
	i := int(receiverShardID)
	for _, hashPath := range merklePath {
		if i%2 == 0 {
			finalHash = common.HashH(append(finalHash.GetBytes(), hashPath.GetBytes()...))
		} else {
			finalHash = common.HashH(append(hashPath.GetBytes(), finalHash.GetBytes()...))
		}
		i = i / 2
	}
	merkleRootString := merkleRoot.String()
	if strings.Compare(finalHash.String(), merkleRootString) != 0 {
		return false
	} else {
		return true
	}
}

/*
	Helper function: group OutputCoin into shard and get the hash of each group
	Return value
		- Array of hash created from 256 group cross shard data hash
		- Length array is 256
		- Value is sorted as shardID from low to high
		- ShardID which have no outputcoin received hash of emptystring value

	Hash Procedure:
		- For each shard:
			CROSS OUTPUT COIN
			+ Get outputcoin and append to a list of that shard
			+ Calculate value for Hash:
				* if receiver shard has no outcoin then received hash value of empty string
				* if receiver shard has >= 1 outcoin then concatenate all outcoin bytes value then hash
				* At last, we compress all cross out put coin into a CrossOutputCoinFinalHash
			TXTOKENDATA
			+ Do the same as above

			=> Then Final Hash of each shard is Hash of value in this order:
				1. CrossOutputCoinFinalHash
				2. TxTokenDataVoutFinalHash
	TxTokenOut DataStructure
		- Use Only One TxTokenData for one TokenID
		- Vouts of one tokenID from many transaction will be compress into One Vouts List
		- Using Key-Value structure for accessing one token ID data:
			key: token ID
			value: TokenData of that token
*/
func getCrossShardDataHash(txList []metadata.Transaction) []common.Hash {
	// group transaction by shardID
	outCoinEachShard := make([][]privacy.OutputCoin, common.MAX_SHARD_NUMBER)
	txTokenDataEachShard := make([]map[common.Hash]*transaction.TxTokenData, common.MAX_SHARD_NUMBER)
	for _, tx := range txList {
		switch tx.GetType() {
		//==================For Constant Transfer Only
		case common.TxNormalType, common.TxSalaryType:
			{
				//==================Proof Process
				if tx.GetProof() != nil {
					for _, outCoin := range tx.GetProof().OutputCoins {
						lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
						shardID := common.GetShardIDFromLastByte(lastByte)
						outCoinEachShard[shardID] = append(outCoinEachShard[shardID], *outCoin)
					}
				}
			}
		//==================For Constant & TxCustomToken Transfer
		case common.TxCustomTokenType:
			{
				customTokenTx := tx.(*transaction.TxCustomToken)
				//==================Proof Process
				if customTokenTx.GetProof() != nil {
					for _, outCoin := range customTokenTx.GetProof().OutputCoins {
						lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
						shardID := common.GetShardIDFromLastByte(lastByte)
						outCoinEachShard[shardID] = append(outCoinEachShard[shardID], *outCoin)
					}
				}
				//==================Tx Token Data Process
				for _, vout := range customTokenTx.TxTokenData.Vouts {
					lastByte := vout.PaymentAddress.Pk[len(vout.PaymentAddress.Pk)-1]
					shardID := common.GetShardIDFromLastByte(lastByte)
					if txTokenDataEachShard[shardID] == nil {
						txTokenDataEachShard[shardID] = make(map[common.Hash]*transaction.TxTokenData)
					}
					if _, ok := txTokenDataEachShard[shardID][customTokenTx.TxTokenData.PropertyID]; !ok {
						newTxTokenData := cloneTxTokenDataForCrossShard(customTokenTx.TxTokenData)
						txTokenDataEachShard[shardID][customTokenTx.TxTokenData.PropertyID] = &newTxTokenData
					}
					vouts := txTokenDataEachShard[shardID][customTokenTx.TxTokenData.PropertyID].Vouts
					vouts = append(vouts, vout)
					txTokenDataEachShard[shardID][customTokenTx.TxTokenData.PropertyID].Vouts = vouts
				}
			}
			// case common.TxCustomTokenPrivacyType:
			// 	{
			// 		customTokenTx := tx.(*transaction.TxCustomTokenPrivacy)
			// 		if customTokenTx.TxTokenPrivacyData.TxNormal.GetProof() != nil {
			// 			for _, outCoin := range customTokenTx.TxTokenPrivacyData.TxNormal.GetProof().OutputCoins {
			// 				lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
			// 				shardID := common.GetShardIDFromLastByte(lastByte)
			// 				byteMap[common.GetShardIDFromLastByte(shardID)] = 1
			// 			}
			// 		}
			// 	}
			// }
		}
	}
	//calcualte hash for each shard
	outputCoinHash := make([]common.Hash, common.MAX_SHARD_NUMBER)
	txTokenOutHash := make([]common.Hash, common.MAX_SHARD_NUMBER)
	combinedHash := make([]common.Hash, common.MAX_SHARD_NUMBER)
	for i := 0; i < common.MAX_SHARD_NUMBER; i++ {
		outputCoinHash[i] = calHashOutCoinCrossShard(outCoinEachShard[i])
		// if len(outCoinEachShard[i]) == 0 {
		// 	outputCoinHash[i] = common.HashH([]byte(""))
		// } else {
		// 	tmpByte := []byte{}
		// 	for _, outCoin := range outCoinEachShard[i] {
		// 		coin := &outCoin
		// 		tmpByte = append(tmpByte, coin.Bytes()...)
		// 	}
		// 	outputCoinHash[i] = common.HashH(tmpByte)
		// }
	}
	for i := 0; i < common.MAX_SHARD_NUMBER; i++ {
		txTokenOutHash[i] = calHashTxTokenDataHashFromMap(txTokenDataEachShard[i])
		// if len(txTokenOutHash[i]) == 0 {
		// 	txTokenOutHash[i] = common.HashH([]byte(""))
		// } else {
		// 	tmpByte := []byte{}
		// 	for _, out := range txTokenOutEachShard[i] {
		// 		tmpByte = append(tmpByte, []byte(out.String())...)
		// 	}
		// 	txTokenOutHash[i] = common.HashH(tmpByte)
		// }
	}
	for i := 0; i < common.MAX_SHARD_NUMBER; i++ {
		combinedHash[i] = common.HashH(append(outputCoinHash[i].GetBytes(), txTokenOutHash[i].GetBytes()...))
	}
	return combinedHash
}

// helper function to get the hash of OutputCoins (send to a shard) from list of transaction
/*
	Get output coin of transaction
	Check receiver last byte
	Append output coin to corresponding shard
*/
func getOutCoinCrossShard(txList []metadata.Transaction, shardID byte) []privacy.OutputCoin {
	coinList := []privacy.OutputCoin{}
	for _, tx := range txList {
		if tx.GetProof() != nil {
			for _, outCoin := range tx.GetProof().OutputCoins {
				lastByte := outCoin.CoinDetails.GetPubKeyLastByte()
				if lastByte == shardID {
					coinList = append(coinList, *outCoin)
				}
			}
		}
	}
	return coinList
}

// helper function to get the hash of OutputCoins (send to a shard) from list of transaction
/*
	Get tx token data of transaction
	Check receiver (in vout) last byte
	Append tx token data to corresponding shard
*/
func getTxTokenDataCrossShard(txList []metadata.Transaction, shardID byte) []transaction.TxTokenData {
	txTokenDataMap := make(map[common.Hash]*transaction.TxTokenData)
	for _, tx := range txList {
		if tx.GetType() == common.TxCustomTokenType {
			customTokenTx := tx.(*transaction.TxCustomToken)
			for _, vout := range customTokenTx.TxTokenData.Vouts {
				lastByte := common.GetShardIDFromLastByte(vout.PaymentAddress.Pk[len(vout.PaymentAddress.Pk)-1])
				if lastByte == shardID {
					if _, ok := txTokenDataMap[customTokenTx.TxTokenData.PropertyID]; !ok {
						newTxTokenData := cloneTxTokenDataForCrossShard(customTokenTx.TxTokenData)
						txTokenDataMap[customTokenTx.TxTokenData.PropertyID] = &newTxTokenData
					}
					vouts := txTokenDataMap[customTokenTx.TxTokenData.PropertyID].Vouts
					vouts = append(vouts, vout)
					txTokenDataMap[customTokenTx.TxTokenData.PropertyID].Vouts = vouts
				}
			}
		}
	}
	var txTokenDataList []transaction.TxTokenData
	if len(txTokenDataMap) != 0 {
		for _, value := range txTokenDataMap {
			txTokenDataList = append(txTokenDataList, *value)
		}
		sort.SliceStable(txTokenDataList[:], func(i, j int) bool {
			return txTokenDataList[i].PropertyID.String() < txTokenDataList[j].PropertyID.String()
		})
	}
	return txTokenDataList
}
func calHashOutCoinCrossShard(outCoins []privacy.OutputCoin) common.Hash {
	tmpByte := []byte{}
	var outputCoinHash common.Hash
	if len(outCoins) != 0 {
		for _, outCoin := range outCoins {
			coin := &outCoin
			tmpByte = append(tmpByte, coin.Bytes()...)
		}
		outputCoinHash = common.HashH(tmpByte)
	} else {
		outputCoinHash = common.HashH([]byte(""))
	}
	return outputCoinHash
}
func calHashTxTokenDataHashFromMap(txTokenDataMap map[common.Hash]*transaction.TxTokenData) common.Hash {
	if len(txTokenDataMap) == 0 {
		return common.HashH([]byte(""))
	}
	var txTokenDataList []transaction.TxTokenData
	for _, value := range txTokenDataMap {
		txTokenDataList = append(txTokenDataList, *value)
	}
	sort.SliceStable(txTokenDataList[:], func(i, j int) bool {
		return txTokenDataList[i].PropertyID.String() < txTokenDataList[j].PropertyID.String()
	})
	return calHashTxTokenDataHashList(txTokenDataList)
}
func calHashTxTokenDataHashList(txTokenDataList []transaction.TxTokenData) common.Hash {
	tmpByte := []byte{}
	for _, txTokenData := range txTokenDataList {
		tempHash, _ := txTokenData.Hash()
		tmpByte = append(tmpByte, tempHash.GetBytes()...)
	}
	return common.HashH(tmpByte)
}
func cloneTxTokenDataForCrossShard(txTokenData transaction.TxTokenData) transaction.TxTokenData {
	newTxTokenData := transaction.TxTokenData{
		PropertyID:     txTokenData.PropertyID,
		PropertyName:   txTokenData.PropertyName,
		PropertySymbol: txTokenData.PropertySymbol,
		Mintable:       txTokenData.Mintable,
		Amount:         txTokenData.Amount,
		Type:           transaction.CustomTokenCrossShard,
	}
	newTxTokenData.Vins = []transaction.TxTokenVin{}
	newTxTokenData.Vouts = []transaction.TxTokenVout{}
	return newTxTokenData
}
func CreateMerkleCrossOutputCoin(crossOutputCoins map[byte][]CrossOutputCoin) (*common.Hash, error) {
	if len(crossOutputCoins) == 0 {
		res, err := GenerateZeroValueHash()

		return &res, err
	}
	keys := []int{}
	crossOutputCoinHashes := []*common.Hash{}
	for k := range crossOutputCoins {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	for _, shardID := range keys {
		for _, value := range crossOutputCoins[byte(shardID)] {
			hash := value.Hash()
			hashByte := hash.GetBytes()
			newHash, err := common.Hash{}.NewHash(hashByte)
			if err != nil {
				return &common.Hash{}, NewBlockChainError(HashError, err)
			}
			crossOutputCoinHashes = append(crossOutputCoinHashes, newHash)
		}
	}
	merkle := Merkle{}
	merkleTree := merkle.BuildMerkleTreeOfHashs(crossOutputCoinHashes)
	return merkleTree[len(merkleTree)-1], nil
}

func VerifyMerkleCrossOutputCoin(crossOutputCoins map[byte][]CrossOutputCoin, rootHash common.Hash) bool {
	res, err := CreateMerkleCrossOutputCoin(crossOutputCoins)
	if err != nil {
		return false
	}
	hashByte := rootHash.GetBytes()
	newHash, err := common.Hash{}.NewHash(hashByte)
	if err != nil {
		return false
	}
	return newHash.IsEqual(res)
}

func (blockchain *BlockChain) StoreIncomingCrossShard(block *ShardBlock) error {
	crossShardMap, _ := block.Body.ExtractIncomingCrossShardMap()
	for crossShard, crossBlks := range crossShardMap {
		for _, crossBlk := range crossBlks {
			blockchain.config.DataBase.StoreIncomingCrossShard(block.Header.ShardID, crossShard, block.Header.Height, &crossBlk)
		}
	}
	return nil
}

//=======================================END CROSS SHARD UTIL
