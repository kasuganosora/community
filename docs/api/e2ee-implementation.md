# E2EE 端到端加密实现文档

## 概述

Community 服务器实现了 Matrix 协议的端到端加密 (E2EE) 功能，支持 Olm 和 Megolm 加密算法。

## 已实现的端点

### 1. 密钥上传

```
POST /_matrix/client/r0/keys/upload
POST /_matrix/client/v3/keys/upload
```

上传设备密钥和一次性密钥 (One-Time Keys)。

**请求体：**
```json
{
  "device_keys": {
    "user_id": "@user:example.com",
    "device_id": "DEVICEID",
    "algorithms": ["m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"],
    "keys": {
      "curve25519:DEVICEID": "base64encodedkey",
      "ed25519:DEVICEID": "base64encodedkey"
    },
    "signatures": {
      "@user:example.com": {
        "ed25519:DEVICEID": "base64signature"
      }
    }
  },
  "one_time_keys": {
    "curve25519:AAAAFw": "base64encodedkey",
    "signed_curve25519:AAAAFw": {
      "key": "base64encodedkey",
      "signatures": {
        "@user:example.com": {
          "ed25519:DEVICEID": "base64signature"
        }
      }
    }
  }
}
```

**响应：**
```json
{
  "one_time_key_counts": {
    "curve25519": 50,
    "signed_curve25519": 50
  }
}
```

### 2. 密钥查询

```
POST /_matrix/client/r0/keys/query
POST /_matrix/client/v3/keys/query
```

查询其他用户的设备密钥。

**请求体：**
```json
{
  "device_keys": {
    "@user1:example.com": [],
    "@user2:example.com": ["DEVICEID1", "DEVICEID2"]
  },
  "timeout": 10000
}
```

**响应：**
```json
{
  "device_keys": {
    "@user1:example.com": {
      "DEVICEID": {
        "user_id": "@user1:example.com",
        "device_id": "DEVICEID",
        "algorithms": ["m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"],
        "keys": {
          "curve25519:DEVICEID": "base64encodedkey",
          "ed25519:DEVICEID": "base64encodedkey"
        },
        "signatures": {
          "@user1:example.com": {
            "ed25519:DEVICEID": "base64signature"
          }
        }
      }
    }
  },
  "failures": {}
}
```

### 3. 密钥认领

```
POST /_matrix/client/r0/keys/claim
POST /_matrix/client/v3/keys/claim
```

认领一次性密钥用于建立 Olm 会话。

**请求体：**
```json
{
  "one_time_keys": {
    "@user:example.com": {
      "DEVICEID": "signed_curve25519"
    }
  },
  "timeout": 10000
}
```

**响应：**
```json
{
  "one_time_keys": {
    "@user:example.com": {
      "DEVICEID": {
        "signed_curve25519:AAAAFw": {
          "key": "base64encodedkey",
          "signatures": {
            "@user:example.com": {
              "ed25519:DEVICEID": "base64signature"
            }
          }
        }
      }
    }
  },
  "failures": {}
}
```

### 4. 一次性密钥数量查询

```
GET /_matrix/client/r0/keys/upload
GET /_matrix/client/v3/keys/upload
```

获取当前存储的一次性密钥数量。

**响应：**
```json
{
  "one_time_key_counts": {
    "curve25519": 50,
    "signed_curve25519": 50
  }
}
```

## 密钥存储

### 数据库表

#### matrix_device_keys
存储设备身份信息。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键 |
| user_id | string | 用户 ID |
| device_id | string | 设备 ID |
| device_key | text | 设备密钥 JSON |
| algorithms | string | 支持的算法 |
| updated_at | datetime | 更新时间 |

#### matrix_one_time_keys
存储一次性密钥。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键 |
| user_id | string | 用户 ID |
| device_id | string | 设备 ID |
| key_id | string | 密钥 ID |
| algorithm | string | 算法 |
| public_key | text | 公钥 |
| used | bool | 是否已使用 |
| created_at | datetime | 创建时间 |

## 安全特性

### 1. 密钥验证
- 设备密钥上传时验证 user_id 和 device_id 匹配
- 验证 algorithms 和 keys 字段存在
- 支持自签名验证

### 2. 密钥签名
- 服务器使用 Ed25519 主密钥对设备密钥进行签名
- 主密钥使用 memguard 安全存储
- 支持交叉签名（Cross-signing）

### 3. 一次性密钥管理
- 密钥认领后自动标记为已使用
- 支持幂等上传（避免重复密钥）
- 自动清理过期密钥

## 联邦 E2EE

### 远程密钥查询
服务器支持通过联邦协议查询远程用户的设备密钥：

```
POST /_matrix/federation/v1/user/keys/query
```

### 远程密钥认领
支持通过联邦协议认领远程用户的一次性密钥：

```
POST /_matrix/federation/v1/user/keys/claim
```

## 使用示例

### 客户端上传密钥

```bash
curl -X POST "https://example.com/_matrix/client/v3/keys/upload" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "device_keys": {
      "user_id": "@user:example.com",
      "device_id": "DEVICEID",
      "algorithms": ["m.olm.v1.curve25519-aes-sha2"],
      "keys": {
        "curve25519:DEVICEID": "base64key",
        "ed25519:DEVICEID": "base64key"
      },
      "signatures": {
        "@user:example.com": {
          "ed25519:DEVICEID": "base64signature"
        }
      }
    }
  }'
```

### 查询其他用户密钥

```bash
curl -X POST "https://example.com/_matrix/client/v3/keys/query" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "device_keys": {
      "@otheruser:example.com": []
    }
  }'
```

## 注意事项

1. **密钥备份**：客户端应实现密钥备份机制
2. **密钥轮换**：建议定期轮换设备密钥
3. **一次性密钥**：保持足够的一次性密钥库存（建议 100+）
4. **联邦支持**：远程用户密钥通过联邦协议获取
