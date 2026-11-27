package model

// UnitConfigVariable — переменная конфига/проекта, передаваемая в activity.
//
// Для hidden-переменных Value пуст, а ValueCipher содержит
// base64(nonce||tag||ciphertext), зашифрованный per-runner ключом.
// Plaintext появляется только в памяти activity после расшифровки.
type UnitConfigVariable struct {
	Id          string
	Value       string
	ValueCipher string
	Hidden      bool
	Type        string
}
