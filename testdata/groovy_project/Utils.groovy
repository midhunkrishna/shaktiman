def formatName(String first, String last) {
    return "${first} ${last}".trim()
}

def validateEmail(String email) {
    return email?.contains('@')
}

int calculateAge(Date birthDate) {
    def now = new Date()
    return now.year - birthDate.year
}
