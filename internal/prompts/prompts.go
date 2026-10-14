// Package prompts is the catalogue of what BitVoice says to callers: each prompt's key and its words
// in every language BitVoice ships. The audio is recorded, uploaded or made by a speech service in the
// web interface, per language, and kept beside the greetings; a prompt without audio is replaced by a
// tone, so a call never waits for speech that is not there.
package prompts

import "slices"

// Prompt is one thing BitVoice says.
type Prompt struct {
	Key   string            `json:"key"`
	Group string            `json:"group"` // the feature that uses it, for the screen
	Text  map[string]string `json:"text"`  // language code -> words
}

// Language is a language BitVoice has the words of.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"` // in the language itself
}

// Languages BitVoice ships the words of. Any other language can be added on the screen: its prompts
// are then recorded or uploaded, with the English words as a guide.
var Languages = []Language{
	{"en", "English"}, {"es", "Español"}, {"fr", "Français"}, {"de", "Deutsch"},
	{"it", "Italiano"}, {"pt", "Português"}, {"nl", "Nederlands"}, {"pl", "Polski"},
}

// Known says whether BitVoice ships the words of a language.
func Known(code string) bool {
	return slices.ContainsFunc(Languages, func(l Language) bool { return l.Code == code })
}

func p(key, group string, en, es, fr, de, it, pt, nl, pl string) Prompt {
	return Prompt{Key: key, Group: group, Text: map[string]string{"en": en, "es": es, "fr": fr, "de": de, "it": it, "pt": pt, "nl": nl, "pl": pl}}
}

// Digit prompts are named digit-0 to digit-9; numbers are said digit by digit, which reads the same
// way in every language.
var digits = [][8]string{
	{"zero", "cero", "zéro", "null", "zero", "zero", "nul", "zero"},
	{"one", "uno", "un", "eins", "uno", "um", "een", "jeden"},
	{"two", "dos", "deux", "zwei", "due", "dois", "twee", "dwa"},
	{"three", "tres", "trois", "drei", "tre", "três", "drie", "trzy"},
	{"four", "cuatro", "quatre", "vier", "quattro", "quatro", "vier", "cztery"},
	{"five", "cinco", "cinq", "fünf", "cinque", "cinco", "vijf", "pięć"},
	{"six", "seis", "six", "sechs", "sei", "seis", "zes", "sześć"},
	{"seven", "siete", "sept", "sieben", "sette", "sete", "zeven", "siedem"},
	{"eight", "ocho", "huit", "acht", "otto", "oito", "acht", "osiem"},
	{"nine", "nueve", "neuf", "neun", "nove", "nove", "negen", "dziewięć"},
}

// All is every prompt, in the order the screen lists them.
var All = func() []Prompt {
	var out []Prompt
	for i, d := range digits {
		out = append(out, p("digit-"+string(rune('0'+i)), "numbers", d[0], d[1], d[2], d[3], d[4], d[5], d[6], d[7]))
	}
	out = append(out,
		p("goodbye", "general", "Goodbye.", "Adiós.", "Au revoir.", "Auf Wiederhören.", "Arrivederci.", "Adeus.", "Tot ziens.", "Do widzenia."),
		p("invalid", "general", "That is not a valid choice.", "Esa opción no es válida.", "Ce choix n'est pas valide.", "Diese Auswahl ist ungültig.", "Scelta non valida.", "Essa opção não é válida.", "Dat is geen geldige keuze.", "To nieprawidłowy wybór."),
		p("thank-you", "general", "Thank you.", "Gracias.", "Merci.", "Danke.", "Grazie.", "Obrigado.", "Dank u.", "Dziękujemy."),
		p("please-hold", "queues", "Please hold. Your call is important to us.", "Por favor, espere. Su llamada es importante para nosotros.", "Veuillez patienter. Votre appel est important pour nous.", "Bitte warten Sie. Ihr Anruf ist uns wichtig.", "Attenda, prego. La sua chiamata è importante per noi.", "Por favor, aguarde. A sua chamada é importante para nós.", "Een moment geduld. Uw oproep is belangrijk voor ons.", "Prosimy czekać. Twoje połączenie jest dla nas ważne."),
		p("queue-position", "queues", "Your place in line is", "Su posición en la cola es", "Votre position dans la file est", "Ihre Position in der Warteschlange ist", "La sua posizione in coda è", "A sua posição na fila é", "Uw plaats in de wachtrij is", "Twoje miejsce w kolejce to"),
		p("queue-wait", "queues", "The expected wait, in minutes, is about", "La espera prevista, en minutos, es de unos", "L'attente prévue, en minutes, est d'environ", "Die erwartete Wartezeit in Minuten beträgt etwa", "L'attesa prevista, in minuti, è di circa", "A espera prevista, em minutos, é de cerca de", "De verwachte wachttijd in minuten is ongeveer", "Przewidywany czas oczekiwania w minutach to około"),
		p("enter-pin", "dial-in", "Please enter your PIN, then press hash.", "Introduzca su PIN y pulse almohadilla.", "Veuillez saisir votre code, puis appuyez sur dièse.", "Bitte geben Sie Ihre PIN ein und drücken Sie die Raute.", "Inserisca il PIN, poi prema cancelletto.", "Introduza o seu PIN e prima cardinal.", "Voer uw pincode in en druk op hekje.", "Wprowadź PIN, a następnie naciśnij krzyżyk."),
		p("pin-wrong", "dial-in", "That PIN is not right.", "Ese PIN no es correcto.", "Ce code n'est pas correct.", "Diese PIN ist nicht richtig.", "Il PIN non è corretto.", "Esse PIN não está correto.", "Die pincode klopt niet.", "Ten PIN jest nieprawidłowy."),
		p("enter-number", "dial-in", "Please enter the number to call, then press hash.", "Marque el número al que desea llamar y pulse almohadilla.", "Composez le numéro à appeler, puis appuyez sur dièse.", "Bitte wählen Sie die gewünschte Nummer und drücken Sie die Raute.", "Componga il numero da chiamare, poi prema cancelletto.", "Marque o número para onde quer ligar e prima cardinal.", "Kies het nummer dat u wilt bellen en druk op hekje.", "Wybierz numer, pod który chcesz zadzwonić, a następnie naciśnij krzyżyk."),
		p("directory-intro", "directory", "Please enter the first three letters of the person's name.", "Introduzca las tres primeras letras del nombre de la persona.", "Saisissez les trois premières lettres du nom de la personne.", "Bitte geben Sie die ersten drei Buchstaben des Namens ein.", "Inserisca le prime tre lettere del nome della persona.", "Introduza as três primeiras letras do nome da pessoa.", "Voer de eerste drie letters van de naam in.", "Wprowadź pierwsze trzy litery nazwiska osoby."),
		p("directory-none", "directory", "No one matches. Please try again.", "No hay coincidencias. Inténtelo de nuevo.", "Aucune personne ne correspond. Veuillez réessayer.", "Niemand passt dazu. Bitte versuchen Sie es noch einmal.", "Nessuna corrispondenza. Riprovi.", "Ninguém corresponde. Tente novamente.", "Niemand komt overeen. Probeer het opnieuw.", "Nikt nie pasuje. Spróbuj ponownie."),
		p("directory-press", "directory", "To call this person, press", "Para llamar a esta persona, pulse", "Pour appeler cette personne, appuyez sur", "Um diese Person anzurufen, drücken Sie die", "Per chiamare questa persona, prema", "Para ligar a esta pessoa, prima", "Om deze persoon te bellen, druk op", "Aby zadzwonić do tej osoby, naciśnij"),
		p("directory-more", "directory", "For more names, press nine.", "Para más nombres, pulse nueve.", "Pour d'autres noms, appuyez sur neuf.", "Für weitere Namen drücken Sie die Neun.", "Per altri nomi, prema nove.", "Para mais nomes, prima nove.", "Voor meer namen, druk op negen.", "Aby usłyszeć więcej nazwisk, naciśnij dziewięć."),
		p("wakeup-menu", "wake-up", "Enter the wake-up time as four digits, for example zero six three zero, or press star to cancel it.", "Introduzca la hora del despertador con cuatro cifras, por ejemplo cero seis tres cero, o pulse asterisco para cancelarlo.", "Saisissez l'heure du réveil en quatre chiffres, par exemple zéro six trois zéro, ou appuyez sur étoile pour l'annuler.", "Geben Sie die Weckzeit vierstellig ein, zum Beispiel null sechs drei null, oder drücken Sie Stern, um sie zu löschen.", "Inserisca l'ora della sveglia con quattro cifre, per esempio zero sei tre zero, o prema asterisco per annullarla.", "Introduza a hora do despertar com quatro dígitos, por exemplo zero seis três zero, ou prima asterisco para a cancelar.", "Voer de wektijd in met vier cijfers, bijvoorbeeld nul zes drie nul, of druk op sterretje om hem te annuleren.", "Wprowadź godzinę budzenia czterema cyframi, na przykład zero sześć trzy zero, lub naciśnij gwiazdkę, aby ją anulować."),
		p("wakeup-set", "wake-up", "Your wake-up call is set for", "Su llamada despertador está programada para las", "Votre réveil est programmé pour", "Ihr Weckruf ist eingestellt auf", "La sua sveglia è impostata per le", "O seu despertar está marcado para as", "Uw wekoproep staat op", "Budzenie ustawiono na"),
		p("wakeup-none", "wake-up", "No wake-up call is set.", "No hay ninguna llamada despertador.", "Aucun réveil n'est programmé.", "Es ist kein Weckruf eingestellt.", "Nessuna sveglia impostata.", "Não há nenhum despertar marcado.", "Er staat geen wekoproep.", "Nie ustawiono budzenia."),
		p("wakeup-cancelled", "wake-up", "Your wake-up call is cancelled.", "Su llamada despertador se ha cancelado.", "Votre réveil est annulé.", "Ihr Weckruf ist gelöscht.", "La sua sveglia è annullata.", "O seu despertar foi cancelado.", "Uw wekoproep is geannuleerd.", "Budzenie zostało anulowane."),
		p("wakeup-call", "wake-up", "Good morning. This is your wake-up call.", "Buenos días. Esta es su llamada despertador.", "Bonjour. Ceci est votre appel de réveil.", "Guten Morgen. Dies ist Ihr Weckruf.", "Buongiorno. Questa è la sua sveglia.", "Bom dia. Esta é a sua chamada de despertar.", "Goedemorgen. Dit is uw wekoproep.", "Dzień dobry. To jest twoje budzenie."),
	)
	return out
}()

// Find returns a prompt by key.
func Find(key string) (Prompt, bool) {
	i := slices.IndexFunc(All, func(p Prompt) bool { return p.Key == key })
	if i < 0 {
		return Prompt{}, false
	}
	return All[i], true
}

// Box is where a prompt's audio is kept, beside the mailbox greetings.
func Box(lang, key string) string { return "prompt-" + lang + "-" + key }

// Digits returns the prompt keys that say a number digit by digit.
func Digits(n string) []string {
	var out []string
	for _, r := range n {
		if r >= '0' && r <= '9' {
			out = append(out, "digit-"+string(r))
		}
	}
	return out
}
