import i18n from "i18next";
import { initReactI18next } from "react-i18next";

const resources = {
  en: {
    translation: {
      welcome: "Welcome back!",
      welcomeSignup: "Create an account",
      subtitleLogin: "Don't have an account yet? ",
      subtitleRegister: "Already have an account? ",
      registerLink: "Register",
      loginLink: "Sign in",
      email: "Email address",
      password: "Password",
      name: "Full Name",
      signIn: "Sign in",
      signUp: "Sign up",
      logout: "Sign out",
      rememberMe: "Remember me",
      forgotPassword: "Forgot password?",
      placeholderName: "Your name",
      placeholderEmail: "hello@vantigo.io",
      placeholderPassword: "Your password",
      errorEmailRequired: "Email is required",
      errorPasswordRequired: "Password is required",
      errorNameRequired: "Name is required",
      accountTitle: "My Account Profile",
      accountSubtitle: "Manage your personal profile and account credentials.",
      profileInfo: "Profile Information",
      appName: "Accounts",
      switcherTitle: "Vantigo Apps",
      unauthorized: "You must be signed in to view this page.",
      redirectToLogin: "Redirecting to login...",
    },
  },
  no: {
    translation: {
      welcome: "Velkommen tilbake!",
      welcomeSignup: "Opprett en konto",
      subtitleLogin: "Har du ikke en konto ennå? ",
      subtitleRegister: "Har du allerede en konto? ",
      registerLink: "Registrer deg",
      loginLink: "Logg inn",
      email: "E-postadresse",
      password: "Passord",
      name: "Fullt navn",
      signIn: "Logg inn",
      signUp: "Registrer deg",
      logout: "Logg ut",
      rememberMe: "Husk meg",
      forgotPassword: "Glemt passord?",
      placeholderName: "Ditt navn",
      placeholderEmail: "hallo@vantigo.io",
      placeholderPassword: "Ditt passord",
      errorEmailRequired: "E-post er påkrevd",
      errorPasswordRequired: "Passord er påkrevd",
      errorNameRequired: "Navn er påkrevd",
      accountTitle: "Min Profil",
      accountSubtitle: "Administrer din profil og innloggingsdetaljer.",
      profileInfo: "Profilinformasjon",
      appName: "Kontoer",
      switcherTitle: "Vantigo-apper",
      unauthorized: "Du må være logget inn for å se denne siden.",
      redirectToLogin: "Sender deg til innlogging...",
    },
  },
};

i18n.use(initReactI18next).init({
  resources,
  lng: "en", // Default language
  fallbackLng: "en",
  interpolation: {
    escapeValue: false, // React already escapes by default
  },
});

// Automatically detect browser language
if (typeof navigator !== "undefined") {
  const browserLang = navigator.language.split("-")[0];
  if (browserLang in resources) {
    i18n.changeLanguage(browserLang);
  }
}

export default i18n;
