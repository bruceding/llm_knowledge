# User Authentication System Design

**Date:** 2026-05-01
**Status:** Approved
**Author:** Claude (based on user requirements discussion)

## Overview

Add user registration and login functionality to the LLM Knowledge system. The system will support multiple users with data isolation, session management with sliding expiration, and captcha-based anti-malicious registration measures.

## Requirements Summary

- **User Mode:** Multi-user system (allow multiple registrations)
- **Anti-Malicious Registration:** Password strength requirements + Captcha
- **Session Strategy:** Sliding expiration (active renewal)
- **Data Isolation:** All data isolated by user (Documents, Conversations, Settings, RSS)
- **Session Storage:** SQLite database
- **Implementation Approach:** Minimal changes (extend existing architecture)

---

## 1. Data Model Design

### 1.1 New Tables

#### User Table
```go
type User struct {
    ID                uint      `gorm:"primaryKey" json:"id"`
    Username          string    `gorm:"unique;not null" json:"username"`
    PasswordHash      string    `gorm:"not null" json:"-"`
    Email             string    `gorm:"unique;not null" json:"email"`
    MustChangePassword bool     `gorm:"default:false" json:"mustChangePassword"`
    CreatedAt         time.Time `json:"createdAt"`
    UpdatedAt         time.Time `json:"updatedAt"`
}
```

**Fields:**
- `Username`: 3-20 characters, unique
- `PasswordHash`: bcrypt hash (cost=10), not returned to frontend
- `Email`: valid email format, unique
- `MustChangePassword`: flag to force password change (used for default user migration and admin password reset)

#### Session Table
```go
type Session struct {
    ID         uint      `gorm:"primaryKey" json:"id"`
    UserID     uint      `gorm:"index;not null" json:"userId"`
    Token      string    `gorm:"unique;not null" json:"token"`
    ExpiresAt  time.Time `gorm:"not null" json:"expiresAt"`
    CreatedAt  time.Time `json:"createdAt"`
    LastAccess time.Time `json:"lastAccess"`
}
```

**Fields:**
- `Token`: UUID-based session token
- `ExpiresAt`: session expiration time (7 days from creation/renewal)
- `LastAccess`: last access time for sliding expiration calculation

#### Captcha Table
```go
type Captcha struct {
    ID        uint      `gorm:"primaryKey"`
    Key       string    `gorm:"unique;not null"`
    Answer    string    `gorm:"not null"`
    ExpiresAt time.Time `gorm:"not null"`
    CreatedAt time.Time
}
```

**Fields:**
- `Key`: UUID captcha key
- `Answer`: 4-character alphanumeric captcha answer
- `ExpiresAt`: 5 minutes expiration

### 1.2 Existing Table Modifications

All existing tables add `UserID` field for data isolation:

```go
type Document struct {
    // ... existing fields
    UserID uint `gorm:"index;not null" json:"userId"`
}

type Conversation struct {
    // ... existing fields
    UserID uint `gorm:"index;not null" json:"userId"`
}

type UserSettings struct {
    // ... existing fields
    UserID uint `gorm:"index;not null" json:"userId"`
}

type RSSFeed struct {
    // ... existing fields
    UserID uint `gorm:"index;not null" json:"userId"`
}

type DocNote struct {
    // ... existing fields
    UserID uint `gorm:"index;not null" json:"userId"`
}
```

---

## 2. API Design

### 2.1 Authentication APIs

#### Get Captcha
```
GET /api/auth/captcha

Response:
{
  "captchaKey": "uuid-string",
  "captchaImage": "base64-encoded-png-image"
}
```

#### Register
```
POST /api/auth/register

Request:
{
  "username": "string (3-20 characters)",
  "password": "string (6-32 characters, must contain letter and digit)",
  "email": "string (valid email format)",
  "captchaKey": "string",
  "captchaAnswer": "string"
}

Response (success):
{
  "success": true,
  "userId": 1
}

Response (error):
{
  "error": "error description",
  "code": "ERROR_CODE"
}
```

#### Login
```
POST /api/auth/login

Request:
{
  "username": "string",
  "password": "string",
  "captchaKey": "string",
  "captchaAnswer": "string"
}

Response (success):
{
  "success": true,
  "token": "session-token-uuid",
  "userId": 1,
  "mustChangePassword": false
}

Response (must change password):
{
  "success": true,
  "token": "session-token-uuid",
  "userId": 1,
  "mustChangePassword": true,
  "message": "请先修改默认密码"
}
```

#### Logout
```
POST /api/auth/logout
Headers: Authorization: Bearer <token>

Response:
{
  "success": true
}
```

#### Check Login Status
```
GET /api/auth/status
Headers: Authorization: Bearer <token>

Response:
{
  "loggedIn": true,
  "userId": 1,
  "username": "john"
}
```

#### Change Password
```
PUT /api/auth/password
Headers: Authorization: Bearer <token>

Request:
{
  "currentPassword": "string",
  "newPassword": "string (6-32 characters, must contain letter and digit)"
}

Response:
{
  "success": true
}
```

### 2.2 Existing API Modifications

All existing APIs require Authorization header:
- Header format: `Authorization: Bearer <session-token>`
- Middleware validates token and auto-renews session
- All queries automatically filter by `UserID`

**Protected API Routes:**
- `/api/documents/*` - Document CRUD
- `/api/conversations/*` - Conversation management
- `/api/query/*` - Query operations
- `/api/settings/*` - User settings
- `/api/rss/*` - RSS feeds
- `/api/doc-chat/*` - Document chat
- `/api/documents/:id/notes/*` - Document notes
- `/api/images/*` - Image upload

**Public API Routes:**
- `/api/auth/*` - Authentication endpoints
- `/api/health` - Health check
- `/api/dependencies/*` - Dependency status
- `/data/*` - Static file access
- `/` - Frontend static pages

---

## 3. Session Management

### 3.1 Session Lifecycle

**Creation (Login):**
- Generate UUID token
- Set `ExpiresAt = Now + 7 days`
- Set `LastAccess = Now`

**Validation (Every Request):**
```
1. Extract token from Authorization header
2. Query Session: WHERE token = ? AND expiresAt > NOW
3. If not found → return 401 Unauthorized
4. If found:
   - Check if Now - LastAccess > 30 minutes
   - If yes → renew: ExpiresAt = Now + 7 days, LastAccess = Now
   - If no → only update LastAccess = Now (avoid frequent DB writes)
5. Inject UserID into request context
```

**Expiration Cleanup:**
- Scheduled task (every hour): `DELETE FROM sessions WHERE expiresAt < NOW`
- User logout: delete specific Session record

### 3.2 Renewal Strategy

**Trigger Condition:**
- Check `LastAccess` on each request
- Renew if more than 30 minutes since last access

**Renewal Logic:**
```
if Now - LastAccess > 30 minutes:
    ExpiresAt = Now + 7 days
    LastAccess = Now
else:
    LastAccess = Now (only update access time)
```

**Concurrent Sessions:**
- One user can have multiple active sessions (multi-device login)
- Each device renews independently
- Logout only deletes current session, not affecting other devices

---

## 4. Password Strength and Captcha

### 4.1 Password Strength Validation

**Rules:**
- Length: 6-32 characters
- Must contain at least 1 letter
- Must contain at least 1 digit
- Special characters: optional (not required)

**Validation Function:**
```go
func validatePassword(password string) error {
    if len(password) < 6 || len(password) > 32 {
        return errors.New("密码长度必须在6-32字符之间")
    }

    hasLetter := false
    hasDigit := false
    for _, c := range password {
        if unicode.IsLetter(c) {
            hasLetter = true
        }
        if unicode.IsDigit(c) {
            hasDigit = true
        }
    }

    if !hasLetter {
        return errors.New("密码必须包含至少一个字母")
    }
    if !hasDigit {
        return errors.New("密码必须包含至少一个数字")
    }

    return nil
}
```

**Password Storage:**
- Use bcrypt hashing with cost=10
- Never store plaintext passwords
- `PasswordHash` field not returned to frontend

### 4.2 Captcha Implementation

**Technology:**
- Library: `github.com/mojocn/base64Captcha`
- Type: 4-character alphanumeric (numbers + letters)
- Case-insensitive verification

**Generation:**
```go
func generateCaptcha() (key string, imageBase64 string) {
    key = uuid.New().String()

    config := base64Captcha.ConfigCharacter{
        Height:          80,
        Width:           240,
        Mode:            base64Captcha.ModeNumberAlphabet,
        ComplexOfNoise:  base64Captcha.ComplexOfNoiseSimple,
        IsShowHollowLine: false,
        IsShowNoiseDot:   true,
        IsShowNoiseText:  false,
        IsShowSlimeLine:  false,
        IsShowSineLine:   false,
        CaptchaLen:       4,
    }

    idKey, cap := base64Captcha.GenerateCaptcha("", config)
    answer := cap.Text

    storeCaptcha(idKey, strings.ToLower(answer), 5*time.Minute)

    return idKey, cap.ToBase64()
}
```

**Verification:**
```go
func verifyCaptcha(key string, answer string) bool {
    captcha := getCaptcha(key)
    if captcha == nil || captcha.ExpiresAt < time.Now() {
        return false
    }

    if strings.ToLower(captcha.Answer) != strings.ToLower(answer) {
        return false
    }

    deleteCaptcha(key)  // prevent reuse
    return true
}
```

**Storage:**
- Store in SQLite `Captcha` table
- Expiration: 5 minutes
- Delete after successful verification (prevent reuse)

---

## 5. Data Migration Strategy

### 5.1 Default User Approach

When upgrading existing system:
1. Create first user (ID=1) as "default user"
2. All existing data `UserID` set to 1
3. Username: "admin", password: system-generated
4. Set `MustChangePassword = true` to force password change on first login

### 5.2 Migration Logic

```go
func Init(path string) error {
    var err error
    DB, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
    if err != nil {
        return err
    }

    // AutoMigrate all tables
    err = DB.AutoMigrate(
        &User{}, &Session{}, &Captcha{},
        &Document{}, &Tag{}, &DocumentTag{},
        &Conversation{}, &ConversationMessage{},
        &UserSettings{}, &RSSFeed{}, &DocNote{},
    )
    if err != nil {
        return err
    }

    // Check if default user needed
    var userCount int64
    DB.Model(&User{}).Count(&userCount)
    if userCount == 0 {
        defaultPassword := generateRandomPassword()
        defaultUser := User{
            Username:          "admin",
            PasswordHash:      hashPassword(defaultPassword),
            Email:             "admin@localhost",
            MustChangePassword: true,
        }
        DB.Create(&defaultUser)

        // Update all existing data to UserID=1
        DB.Model(&Document{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
        DB.Model(&Conversation{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
        DB.Model(&UserSettings{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
        DB.Model(&RSSFeed{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
        DB.Model(&DocNote{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)

        log.Printf("Created default user 'admin', password: %s", defaultPassword)
    }

    return nil
}
```

### 5.3 MustChangePassword Field Usage

The `MustChangePassword` field is a generic mechanism:
- **Default user migration:** Force password change on first login
- **Admin password reset:** Reset user password and force change
- **Security policy:** Batch force password change for security concerns

---

## 6. Authentication Middleware

### 6.1 Middleware Implementation

```go
func AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
    return func(c echo.Context) error {
        authHeader := c.Request().Header.Get("Authorization")
        if authHeader == "" {
            return c.JSON(401, map[string]string{"error": "未登录"})
        }

        token := strings.TrimPrefix(authHeader, "Bearer ")
        if token == authHeader {
            return c.JSON(401, map[string]string{"error": "无效的认证格式"})
        }

        var session Session
        result := DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&session)
        if result.Error != nil {
            return c.JSON(401, map[string]string{"error": "Session无效或已过期"})
        }

        now := time.Now()
        if now.Sub(session.LastAccess) > 30*time.Minute {
            session.ExpiresAt = now.Add(7 * 24 * time.Hour)
            session.LastAccess = now
            DB.Save(&session)
        } else {
            session.LastAccess = now
            DB.Save(&session)
        }

        c.Set("userId", session.UserID)
        return next(c)
    }
}
```

### 6.2 Middleware Application Strategy

**Route Group Approach (Recommended):**
```go
// Public routes (no auth required)
e.GET("/api/auth/captcha", authH.GetCaptcha)
e.POST("/api/auth/register", authH.Register)
e.POST("/api/auth/login", authH.Login)
e.GET("/api/health", healthH.Check)

// Protected routes (auth required)
apiGroup := e.Group("/api")
apiGroup.Use(AuthMiddleware)
apiGroup.GET("/documents", docH.ListAll)
apiGroup.GET("/documents/:id", docH.GetDoc)
apiGroup.POST("/documents/:id/publish", docH.Publish)
// ... other protected routes
```

---

## 7. Handler Layer UserID Handling

### 7.1 Helper Function

```go
func GetCurrentUserId(c echo.Context) uint {
    userId, ok := c.Get("userId").(uint)
    if !ok {
        return 0
    }
    return userId
}
```

### 7.2 Handler Modification Examples

**Document Handler:**
```go
func (h *DocHandler) ListAll(c echo.Context) error {
    userId := GetCurrentUserId(c)
    var docs []Document
    DB.Where("user_id = ?", userId).Find(&docs)
    return c.JSON(200, docs)
}

func (h *DocHandler) Create(c echo.Context) error {
    userId := GetCurrentUserId(c)
    doc := Document{
        Title:  "新文档",
        UserID: userId,
    }
    DB.Create(&doc)
    return c.JSON(200, doc)
}

func (h *DocHandler) DeleteDoc(c echo.Context) error {
    userId := GetCurrentUserId(c)
    id := c.Param("id")

    result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Document{})
    if result.RowsAffected == 0 {
        return c.JSON(404, map[string]string{"error": "文档不存在或无权删除"})
    }

    return c.JSON(200, map[string]bool{"success": true})
}
```

**Key Security Principle:**
- All queries must include `user_id = ?` condition
- Prevent cross-user data access
- Validate `user_id` on update/delete operations

---

## 8. Frontend Design

### 8.1 Page Flow

```
User visits any page → Check login status → Not logged in → Redirect to login page
                                                      ↓
                                               Check MustChangePassword
                                                      ↓
                                               Yes → Redirect to change password page
                                               No → Normal system usage
```

### 8.2 Page Elements

**Login Page:**
- Username input
- Password input
- Captcha image + input
- "Register" link
- "Login" button

**Register Page:**
- Username input (3-20 characters)
- Email input
- Password input (6-32 characters, hint: must contain letter and digit)
- Confirm password input
- Captcha image + input
- "Already have account? Login" link
- "Register" button

**Change Password Page:**
- Current password input (show hint for default password)
- New password input (with strength hint)
- Confirm new password input
- "Submit" button
- Redirect to main page after success

**Design Style:**
- Simple and clean
- Elegant and beautiful
- User-friendly form validation

### 8.3 State Management

```typescript
interface AuthState {
  isLoggedIn: boolean;
  userId: number | null;
  username: string | null;
  mustChangePassword: boolean;
  token: string | null;
}

const useAuthStore = create<AuthState>(() => ({
  isLoggedIn: false,
  userId: null,
  username: null,
  mustChangePassword: false,
  token: null,
}));
```

### 8.4 API Interceptors

```typescript
// Request interceptor - add token to all requests
axios.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

// Response interceptor - handle 401 unauthorized
axios.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      useAuthStore.setState({ isLoggedIn: false, token: null });
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);
```

### 8.5 Route Guard

```typescript
function PrivateRoute({ children }: { children: React.ReactNode }) {
  const { isLoggedIn, mustChangePassword } = useAuthStore();

  if (!isLoggedIn) {
    return <Navigate to="/login" replace />;
  }

  if (mustChangePassword) {
    return <Navigate to="/change-password" replace />;
  }

  return <>{children}</>;
}

// Apply to all protected pages
<Route path="/documents" element={<PrivateRoute><DocumentsPage /></PrivateRoute>} />
<Route path="/inbox" element={<PrivateRoute><InboxPage /></PrivateRoute>} />
```

---

## 9. Error Handling

### 9.1 Registration Errors

- Username already exists → "用户名已被使用"
- Email already exists → "邮箱已被注册"
- Username length invalid → "用户名长度需在3-20字符之间"
- Password strength insufficient → "密码必须包含至少一个字母和一个数字，长度6-32字符"
- Captcha error → "验证码错误或已过期"
- Captcha expired → "验证码已过期，请重新获取"

### 9.2 Login Errors

- Username not found → "用户名或密码错误" (don't reveal which is wrong)
- Password incorrect → "用户名或密码错误"
- Captcha error → "验证码错误或已过期"

### 9.3 Session Errors

- Session expired → 401 + "登录已过期，请重新登录"
- Token invalid → 401 + "无效的认证信息"
- Permission denied → 403 + "无权访问此资源"

### 9.4 Global Error Response Format

```json
{
  "error": "错误描述",
  "code": "ERROR_CODE"
}
```

---

## 10. Testing Strategy

### 10.1 Unit Tests

**Password Validation:**
```go
func TestValidatePassword(t *testing.T) {
    tests := []struct {
        password string
        valid    bool
    }{
        {"abc123", true},
        {"123456", false},
        {"abcdef", false},
        {"ab1", false},
        {"a123456789012345678901234567890123", false},
    }
    // ...
}
```

**Captcha:**
```go
func TestCaptcha(t *testing.T) {
    key, answer := generateCaptcha()
    assert.True(t, verifyCaptcha(key, answer))
    assert.False(t, verifyCaptcha(key, "wrong"))
    assert.False(t, verifyCaptcha("invalid", answer))
}
```

**Session Renewal:**
```go
func TestSessionRenewal(t *testing.T) {
    // Create session
    // Simulate request after 30+ minutes
    // Verify ExpiresAt renewed correctly
}
```

### 10.2 Integration Tests

**Registration Login Flow:**
```go
func TestRegisterLoginFlow(t *testing.T) {
    // 1. Get captcha
    // 2. Register user
    // 3. Login to get token
    // 4. Access API with token
    // 5. Verify data isolation
}
```

**Data Isolation:**
```go
func TestDataIsolation(t *testing.T) {
    // Create two users
    // User A creates document
    // User B tries to access User A's document → should return 404/403
}
```

---

## Summary

This design provides a complete user authentication system with:
- **Secure password handling** (bcrypt + strength validation)
- **Anti-malicious registration** (captcha protection)
- **Session management** (SQLite storage + sliding expiration)
- **Data isolation** (all data filtered by UserID)
- **Safe migration** (default user with forced password change)
- **Clean frontend** (simple, elegant login/register pages)

The implementation follows the "minimal changes" principle, extending the existing SQLite + GORM + Echo architecture without introducing unnecessary complexity.