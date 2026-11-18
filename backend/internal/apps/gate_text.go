package apps

import "strings"

// The sign-in page speaks the visitor's language among Syno Connect Center's nine.
// Lines carry no trailing period, as in the rest of Syno Connect Center.
var gateTexts = map[string]map[string]string{
	"en": {
		"title": "Sign in to {app}", "lead": "Use your DSM account on this NAS", "account": "Account", "password": "Password",
		"otp": "Verification code", "submit": "Sign in", "footer": "Syno Connect Center checks your DSM sign-in before it opens this App",
		"error.invalid_credentials": "The account or password is incorrect", "error.otp_required": "Enter the verification code from your authenticator app",
		"error.otp_invalid": "The verification code is incorrect", "error.account_disabled": "This account is disabled",
		"error.blocked": "DSM blocked this sign-in for now; try again later", "error.not_permitted": "This account cannot sign in to DSM",
		"error.too_many": "Too many attempts; wait a few minutes and try again", "error.unavailable": "DSM did not answer; try again",
	},
	"zh-TW": {
		"title": "登入 {app}", "lead": "請使用這台 NAS 的 DSM 帳號", "account": "帳號", "password": "密碼",
		"otp": "驗證碼", "submit": "登入", "footer": "Syno Connect Center 會先確認你的 DSM 登入，再開啟這個 App",
		"error.invalid_credentials": "帳號或密碼不正確", "error.otp_required": "請輸入驗證器 App 中的驗證碼",
		"error.otp_invalid": "驗證碼不正確", "error.account_disabled": "這個帳號已停用",
		"error.blocked": "DSM 暫時封鎖了這次登入，請稍後再試", "error.not_permitted": "這個帳號無法登入 DSM",
		"error.too_many": "嘗試次數過多，請等幾分鐘再試", "error.unavailable": "DSM 沒有回應，請再試一次",
	},
	"zh-CN": {
		"title": "登录 {app}", "lead": "请使用这台 NAS 的 DSM 账号", "account": "账号", "password": "密码",
		"otp": "验证码", "submit": "登录", "footer": "Syno Connect Center 会先确认你的 DSM 登录，再打开这个 App",
		"error.invalid_credentials": "账号或密码不正确", "error.otp_required": "请输入验证器 App 中的验证码",
		"error.otp_invalid": "验证码不正确", "error.account_disabled": "这个账号已停用",
		"error.blocked": "DSM 暂时阻止了这次登录，请稍后再试", "error.not_permitted": "这个账号无法登录 DSM",
		"error.too_many": "尝试次数过多，请等几分钟再试", "error.unavailable": "DSM 没有响应，请再试一次",
	},
	"ja": {
		"title": "{app} にサインイン", "lead": "この NAS の DSM アカウントを使用します", "account": "アカウント", "password": "パスワード",
		"otp": "確認コード", "submit": "サインイン", "footer": "Syno Connect Center は DSM のサインインを確認してからこの App を開きます",
		"error.invalid_credentials": "アカウントまたはパスワードが正しくありません", "error.otp_required": "認証アプリの確認コードを入力してください",
		"error.otp_invalid": "確認コードが正しくありません", "error.account_disabled": "このアカウントは無効です",
		"error.blocked": "DSM がこのサインインを一時的にブロックしました。しばらくしてから再試行してください", "error.not_permitted": "このアカウントは DSM にサインインできません",
		"error.too_many": "試行回数が多すぎます。数分待ってから再試行してください", "error.unavailable": "DSM から応答がありません。もう一度お試しください",
	},
	"ko": {
		"title": "{app}에 로그인", "lead": "이 NAS의 DSM 계정을 사용하세요", "account": "계정", "password": "비밀번호",
		"otp": "인증 코드", "submit": "로그인", "footer": "Syno Connect Center는 DSM 로그인을 확인한 후 이 앱을 엽니다",
		"error.invalid_credentials": "계정 또는 비밀번호가 올바르지 않습니다", "error.otp_required": "인증 앱의 인증 코드를 입력하세요",
		"error.otp_invalid": "인증 코드가 올바르지 않습니다", "error.account_disabled": "이 계정은 비활성화되어 있습니다",
		"error.blocked": "DSM이 이 로그인을 잠시 차단했습니다. 나중에 다시 시도하세요", "error.not_permitted": "이 계정은 DSM에 로그인할 수 없습니다",
		"error.too_many": "시도 횟수가 너무 많습니다. 몇 분 후에 다시 시도하세요", "error.unavailable": "DSM이 응답하지 않습니다. 다시 시도하세요",
	},
	"de": {
		"title": "Bei {app} anmelden", "lead": "Verwenden Sie Ihr DSM-Konto auf diesem NAS", "account": "Konto", "password": "Passwort",
		"otp": "Bestätigungscode", "submit": "Anmelden", "footer": "Syno Connect Center prüft Ihre DSM-Anmeldung, bevor diese App geöffnet wird",
		"error.invalid_credentials": "Konto oder Passwort ist falsch", "error.otp_required": "Geben Sie den Bestätigungscode aus Ihrer Authentifizierungs-App ein",
		"error.otp_invalid": "Der Bestätigungscode ist falsch", "error.account_disabled": "Dieses Konto ist deaktiviert",
		"error.blocked": "DSM hat diese Anmeldung vorübergehend blockiert; versuchen Sie es später erneut", "error.not_permitted": "Dieses Konto kann sich nicht bei DSM anmelden",
		"error.too_many": "Zu viele Versuche; warten Sie einige Minuten und versuchen Sie es erneut", "error.unavailable": "DSM hat nicht geantwortet; versuchen Sie es erneut",
	},
	"fr": {
		"title": "Se connecter à {app}", "lead": "Utilisez votre compte DSM de ce NAS", "account": "Compte", "password": "Mot de passe",
		"otp": "Code de vérification", "submit": "Se connecter", "footer": "Syno Connect Center vérifie votre connexion DSM avant d'ouvrir cette App",
		"error.invalid_credentials": "Le compte ou le mot de passe est incorrect", "error.otp_required": "Saisissez le code de vérification de votre application d'authentification",
		"error.otp_invalid": "Le code de vérification est incorrect", "error.account_disabled": "Ce compte est désactivé",
		"error.blocked": "DSM a bloqué cette connexion pour le moment ; réessayez plus tard", "error.not_permitted": "Ce compte ne peut pas se connecter à DSM",
		"error.too_many": "Trop de tentatives ; patientez quelques minutes et réessayez", "error.unavailable": "DSM n'a pas répondu ; réessayez",
	},
	"es": {
		"title": "Iniciar sesión en {app}", "lead": "Use su cuenta de DSM de este NAS", "account": "Cuenta", "password": "Contraseña",
		"otp": "Código de verificación", "submit": "Iniciar sesión", "footer": "Syno Connect Center comprueba su inicio de sesión de DSM antes de abrir esta App",
		"error.invalid_credentials": "La cuenta o la contraseña no son correctas", "error.otp_required": "Introduzca el código de verificación de su app de autenticación",
		"error.otp_invalid": "El código de verificación no es correcto", "error.account_disabled": "Esta cuenta está deshabilitada",
		"error.blocked": "DSM bloqueó este inicio de sesión por ahora; inténtelo más tarde", "error.not_permitted": "Esta cuenta no puede iniciar sesión en DSM",
		"error.too_many": "Demasiados intentos; espere unos minutos e inténtelo de nuevo", "error.unavailable": "DSM no respondió; inténtelo de nuevo",
	},
	"it": {
		"title": "Accedi a {app}", "lead": "Usa il tuo account DSM di questo NAS", "account": "Account", "password": "Password",
		"otp": "Codice di verifica", "submit": "Accedi", "footer": "Syno Connect Center verifica l'accesso a DSM prima di aprire questa App",
		"error.invalid_credentials": "Account o password non corretti", "error.otp_required": "Inserisci il codice di verifica dell'app di autenticazione",
		"error.otp_invalid": "Il codice di verifica non è corretto", "error.account_disabled": "Questo account è disattivato",
		"error.blocked": "DSM ha bloccato questo accesso per ora; riprova più tardi", "error.not_permitted": "Questo account non può accedere a DSM",
		"error.too_many": "Troppi tentativi; attendi qualche minuto e riprova", "error.unavailable": "DSM non ha risposto; riprova",
	},
}

func gateText(locale string) map[string]string {
	if t, ok := gateTexts[locale]; ok {
		return t
	}
	return gateTexts["en"]
}

// pageLocale picks the first supported language in Accept-Language.
func pageLocale(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(tag)
		switch {
		case tag == "zh-tw" || tag == "zh-hk" || tag == "zh-mo" || strings.HasPrefix(tag, "zh-hant"):
			return "zh-TW"
		case strings.HasPrefix(tag, "zh"):
			return "zh-CN"
		}
		base, _, _ := strings.Cut(tag, "-")
		if _, ok := gateTexts[base]; ok {
			return base
		}
	}
	return "en"
}
