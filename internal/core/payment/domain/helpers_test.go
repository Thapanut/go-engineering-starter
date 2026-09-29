package domain_test

import "github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"

func thb(v int64) domain.Money { return domain.Money{Amount: v, Currency: domain.THB} }
