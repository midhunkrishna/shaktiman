import groovy.transform.CompileStatic

@CompileStatic
class UserService {
    List<Map> users = []

    void addUser(String name, String email) {
        users << [name: name, email: email]
    }

    Map findUser(String name) {
        return users.find { it.name == name }
    }

    boolean removeUser(String name) {
        return users.removeAll { it.name == name }
    }

    int getUserCount() {
        return users.size()
    }
}
